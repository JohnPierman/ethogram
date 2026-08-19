//go:build integration

// The suite is behind the integration build tag so the default suite runs without a
// database (README: database-dependent tests are tagged). It connects to the compose
// stack's Postgres (env override PG_URL) and isolates each run in a throwaway schema.
package postgres_test

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/JohnPierman/ethogram/domain/cooccurrence"
	"github.com/JohnPierman/ethogram/domain/event"
	"github.com/JohnPierman/ethogram/domain/novelty"
	"github.com/JohnPierman/ethogram/domain/timing"
	"github.com/JohnPierman/ethogram/domain/volume"
	"github.com/JohnPierman/ethogram/infrastructure/state/memory"
	"github.com/JohnPierman/ethogram/infrastructure/state/postgres"
)

const (
	defaultPGURL = "postgres://cad:cad_dev_only@127.0.0.1:55432/cad"
	testHalfLife = novelty.HalfLife(36 * event.Hour)
)

var (
	itConnString string
	itSkipReason string
)

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

// testMain creates one dedicated schema named it_<unix-nanos> for the whole run and
// drops it in cleanup, so repeated runs never collide. When the database is
// unreachable every test skips with the recorded reason instead of failing.
func testMain(m *testing.M) int {
	base := os.Getenv("PG_URL")
	if base == "" {
		base = defaultPGURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	admin, err := pgxpool.New(ctx, base)
	if err != nil {
		itSkipReason = fmt.Sprintf("postgres unreachable (parse %q): %v — start it with `docker compose up -d`", base, err)
		return m.Run()
	}
	defer admin.Close()
	if pingErr := admin.Ping(ctx); pingErr != nil {
		itSkipReason = fmt.Sprintf("postgres unreachable at %q: %v — start it with `docker compose up -d`", base, err)
		return m.Run()
	}

	schema := fmt.Sprintf("it_%d", time.Now().UnixNano())
	if _, execErr := admin.Exec(ctx, "CREATE SCHEMA "+schema); execErr != nil {
		itSkipReason = fmt.Sprintf("create schema %s: %v", schema, err)
		return m.Run()
	}
	defer func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer dropCancel()
		if _, dropErr := admin.Exec(dropCtx, "DROP SCHEMA "+schema+" CASCADE"); dropErr != nil {
			fmt.Fprintf(os.Stderr, "drop schema %s: %v\n", schema, dropErr)
		}
	}()

	conn, err := withSearchPath(base, schema)
	if err != nil {
		itSkipReason = fmt.Sprintf("build connection string: %v", err)
		return m.Run()
	}
	itConnString = conn
	return m.Run()
}

// withSearchPath pins the connection's search_path to the run's schema, so the
// store's unqualified table names land in the isolated namespace.
func withSearchPath(base, schema string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse %q: %w", base, err)
	}
	q := u.Query()
	q.Set("options", "-csearch_path="+schema)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// newStore opens a store against the run's schema, applying migrations, and closes it
// with the test.
func newStore(t *testing.T) *postgres.Store {
	t.Helper()
	if itSkipReason != "" {
		t.Skip(itSkipReason)
	}
	s, err := postgres.New(context.Background(), itConnString, testHalfLife)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

// mustSameBits asserts bit-for-bit float equality, the R4 standard: approximate
// equality would hide exactly the drift this suite exists to catch.
func mustSameBits(t *testing.T, label string, mem, pg float64) {
	t.Helper()
	if math.Float64bits(mem) != math.Float64bits(pg) {
		t.Fatalf("%s: memory %v (%#x) != postgres %v (%#x)",
			label, mem, math.Float64bits(mem), pg, math.Float64bits(pg))
	}
}

type coEdge struct {
	a, b cooccurrence.NodeID
}

// TestCrossImplementationEquivalence drives the same deterministic sequence of 200
// mixed observations — 3 entities, 2 fields, irregular timestamps — into the memory
// stores and the Postgres store, then requires every read to agree bit-for-bit at 3
// probe timestamps, including row order. This is the test that matters: it proves the
// Postgres store is a drop-in replacement under R4.
func TestCrossImplementationEquivalence(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()

	memNovelty := memory.NewNoveltyStore(testHalfLife)
	memTiming := memory.NewTimingStore()
	memVolume := memory.NewVolumeStore()
	memGraph := cooccurrence.NewMemoryGraph(testHalfLife)

	pgNovelty := store.Novelty()
	pgTiming := store.Timing()
	pgVolume := store.Volume()
	pgGraph := store.Graph()

	src := event.SourceID("it-equiv")
	entities := []event.EntityID{"acct-alpha", "acct-beta", "acct-gamma"}
	fields := []event.FieldPath{"auth.type", "dst.host"}
	const observations = 200
	const order = 4 // harmonics for the timing fold; the 2H+1 = 23 case is TestTimingStateRoundTrip's

	nodesSeen := map[cooccurrence.NodeID]struct{}{}
	edgesSeen := map[coEdge]struct{}{}

	ts := event.Timestamp(1_700_000_000) * event.Second
	var last event.Timestamp
	for i := 0; i < observations; i++ {
		// Irregular spacing from coprime multipliers: aperiodic gaps without
		// randomness, so the sequence is deterministic (R4).
		ts += event.Timestamp(i%7+1)*13*event.Minute +
			event.Timestamp(i%11)*17*event.Second +
			event.Timestamp(i%3)*997*event.Microsecond
		last = ts
		en := entities[i%len(entities)]
		f := fields[i%len(fields)]
		value := fmt.Sprintf("value-%02d", (i*i+3*i)%17)

		if err := memNovelty.SaveObservation(ctx, src, en, f, value, ts); err != nil {
			t.Fatalf("memory novelty save %d: %v", i, err)
		}
		if err := pgNovelty.SaveObservation(ctx, src, en, f, value, ts); err != nil {
			t.Fatalf("postgres novelty save %d: %v", i, err)
		}

		a := cooccurrence.NodeID{Field: fields[0], Value: value}
		b := cooccurrence.NodeID{Field: fields[1], Value: fmt.Sprintf("peer-%d", i%5)}
		nodesSeen[a], nodesSeen[b] = struct{}{}, struct{}{}
		edgesSeen[coEdge{a: a, b: b}] = struct{}{}
		ga, gb := a, b
		if i%3 == 1 {
			ga, gb = b, a // exercise edge canonicalisation on the write path
		}
		if err := memGraph.SaveCoOccurrence(ctx, src, ga, gb, ts); err != nil {
			t.Fatalf("memory graph save %d: %v", i, err)
		}
		if err := pgGraph.SaveCoOccurrence(ctx, src, ga, gb, ts); err != nil {
			t.Fatalf("postgres graph save %d: %v", i, err)
		}

		// Timing and volume are read-fold-save through each store independently:
		// bit equality of the final states proves the round trip is lossless.
		foldTiming(t, ctx, memTiming, src, en, ts, order)
		foldTiming(t, ctx, pgTiming, src, en, ts, order)
		foldVolume(t, ctx, memVolume, src, en, ts)
		foldVolume(t, ctx, pgVolume, src, en, ts)
	}

	probes := []event.Timestamp{
		last,
		last + 3*event.Hour + 1_234_567,
		last + 9*event.Day + 987_654_321,
	}

	for _, probe := range probes {
		for _, en := range entities {
			for _, f := range fields {
				memRows, err := memNovelty.FindAllByEntityField(ctx, src, en, f, probe)
				if err != nil {
					t.Fatalf("memory novelty find (%s, %s): %v", en, f, err)
				}
				pgRows, err := pgNovelty.FindAllByEntityField(ctx, src, en, f, probe)
				if err != nil {
					t.Fatalf("postgres novelty find (%s, %s): %v", en, f, err)
				}
				if len(memRows) != len(pgRows) {
					t.Fatalf("probe %d (%s, %s): %d memory rows, %d postgres rows",
						probe, en, f, len(memRows), len(pgRows))
				}
				for i := range memRows {
					m, p := memRows[i], pgRows[i]
					if m.Value != p.Value {
						t.Fatalf("probe %d (%s, %s) row %d: value %q (memory) != %q (postgres) — row order must agree",
							probe, en, f, i, m.Value, p.Value)
					}
					mustSameBits(t, fmt.Sprintf("count(%s, %s, %s) at %d", en, f, m.Value, probe), m.Count, p.Count)
					if m.FirstSeen != p.FirstSeen || m.LastSeen != p.LastSeen {
						t.Fatalf("probe %d (%s, %s, %s): seen (%d, %d) memory vs (%d, %d) postgres",
							probe, en, f, m.Value, m.FirstSeen, m.LastSeen, p.FirstSeen, p.LastSeen)
					}
				}
			}
		}

		for _, e := range sortedEdges(edgesSeen) {
			mw, err := memGraph.FindEdgeWeight(ctx, src, e.a, e.b, probe)
			if err != nil {
				t.Fatalf("memory edge weight: %v", err)
			}
			pw, err := pgGraph.FindEdgeWeight(ctx, src, e.a, e.b, probe)
			if err != nil {
				t.Fatalf("postgres edge weight: %v", err)
			}
			mustSameBits(t, fmt.Sprintf("edge (%v, %v) at %d", e.a, e.b, probe), mw, pw)

			// The reversed query must address the same canonical row.
			pwReversed, err := pgGraph.FindEdgeWeight(ctx, src, e.b, e.a, probe)
			if err != nil {
				t.Fatalf("postgres reversed edge weight: %v", err)
			}
			mustSameBits(t, fmt.Sprintf("reversed edge (%v, %v) at %d", e.b, e.a, probe), mw, pwReversed)
		}
		for _, n := range sortedNodes(nodesSeen) {
			md, err := memGraph.FindDegree(ctx, src, n, probe)
			if err != nil {
				t.Fatalf("memory degree: %v", err)
			}
			pd, err := pgGraph.FindDegree(ctx, src, n, probe)
			if err != nil {
				t.Fatalf("postgres degree: %v", err)
			}
			mustSameBits(t, fmt.Sprintf("degree %v at %d", n, probe), md, pd)
		}
		mTotal, err := memGraph.FindTotalWeight(ctx, src, probe)
		if err != nil {
			t.Fatalf("memory total weight: %v", err)
		}
		pTotal, err := pgGraph.FindTotalWeight(ctx, src, probe)
		if err != nil {
			t.Fatalf("postgres total weight: %v", err)
		}
		mustSameBits(t, fmt.Sprintf("total weight at %d", probe), mTotal, pTotal)
	}

	// Timing and volume state reads take no probe time; the final states must agree.
	for _, en := range entities {
		mSt, mOK, err := memTiming.FindByEntity(ctx, src, en)
		if err != nil {
			t.Fatalf("memory timing find %s: %v", en, err)
		}
		pSt, pOK, err := pgTiming.FindByEntity(ctx, src, en)
		if err != nil {
			t.Fatalf("postgres timing find %s: %v", en, err)
		}
		if !mOK || !pOK {
			t.Fatalf("timing state for %s: ok = %v (memory), %v (postgres)", en, mOK, pOK)
		}
		if mSt.Moments.H() != pSt.Moments.H() {
			t.Fatalf("timing H for %s: %d (memory) != %d (postgres)", en, mSt.Moments.H(), pSt.Moments.H())
		}
		mustSameBits(t, fmt.Sprintf("timing W(%s)", en), mSt.Moments.W, pSt.Moments.W)
		for h := 0; h < mSt.Moments.H(); h++ {
			mustSameBits(t, fmt.Sprintf("timing C[%d](%s)", h, en), mSt.Moments.C[h], pSt.Moments.C[h])
			mustSameBits(t, fmt.Sprintf("timing S[%d](%s)", h, en), mSt.Moments.S[h], pSt.Moments.S[h])
		}
		if mSt.LastSeen != pSt.LastSeen {
			t.Fatalf("timing last seen for %s: %d (memory) != %d (postgres)", en, mSt.LastSeen, pSt.LastSeen)
		}

		mVol, mOK, err := memVolume.FindByEntity(ctx, src, en)
		if err != nil {
			t.Fatalf("memory volume find %s: %v", en, err)
		}
		pVol, pOK, err := pgVolume.FindByEntity(ctx, src, en)
		if err != nil {
			t.Fatalf("postgres volume find %s: %v", en, err)
		}
		if !mOK || !pOK {
			t.Fatalf("volume state for %s: ok = %v (memory), %v (postgres)", en, mOK, pOK)
		}
		mustSameBits(t, fmt.Sprintf("volume a(%s)", en), mVol.Rate.A, pVol.Rate.A)
		mustSameBits(t, fmt.Sprintf("volume b(%s)", en), mVol.Rate.B, pVol.Rate.B)
		if mVol.PeriodIndex != pVol.PeriodIndex || mVol.PeriodCount != pVol.PeriodCount ||
			mVol.WindowIndex != pVol.WindowIndex || mVol.WindowCount != pVol.WindowCount ||
			mVol.LastSeen != pVol.LastSeen {
			t.Fatalf("volume counters for %s: %+v (memory) != %+v (postgres)", en, mVol, pVol)
		}
	}
}

// foldTiming applies the equation (6) commit fold through the given repository,
// mirroring the domain observation's Commit.
func foldTiming(t *testing.T, ctx context.Context, repo timing.StateRepository, src event.SourceID, en event.EntityID, at event.Timestamp, order int) {
	t.Helper()
	st, ok, err := repo.FindByEntity(ctx, src, en)
	if err != nil {
		t.Fatalf("timing find for fold: %v", err)
	}
	if !ok {
		st = &timing.State{Moments: timing.NewMoments(order)}
	}
	delta := 1.0
	if st.Moments.W > 0 {
		delta = novelty.DecayFactor(st.LastSeen, at, testHalfLife)
	}
	st.Moments.Observe(timing.PhaseOfTimestamp(at), delta)
	if at > st.LastSeen {
		st.LastSeen = at
	}
	if err := repo.SaveState(ctx, src, en, st); err != nil {
		t.Fatalf("timing save for fold: %v", err)
	}
}

// foldVolume applies the §7.4 commit fold through the given repository, mirroring the
// domain observation's Commit: completed periods into the posterior, then the running
// counters.
func foldVolume(t *testing.T, ctx context.Context, repo volume.StateRepository, src event.SourceID, en event.EntityID, at event.Timestamp) {
	t.Helper()
	day := int64(at / event.Day)
	hour := int64(at / event.Hour)
	st, ok, err := repo.FindByEntity(ctx, src, en)
	if err != nil {
		t.Fatalf("volume find for fold: %v", err)
	}
	if !ok {
		st = &volume.State{PeriodIndex: day, WindowIndex: hour}
	}
	delta := math.Exp2(-float64(event.Day) / float64(testHalfLife))
	if day > st.PeriodIndex {
		a, b := st.Rate.A, st.Rate.B
		a = delta*a + float64(st.PeriodCount)
		b = delta*b + 1
		for p := st.PeriodIndex + 1; p < day; p++ {
			a = delta * a
			b = delta*b + 1
		}
		st.Rate = volume.GammaPosterior{A: a, B: b}
		st.PeriodIndex = day
		st.PeriodCount = 0
	}
	st.PeriodCount++
	if hour != st.WindowIndex {
		st.WindowIndex = hour
		st.WindowCount = 0
	}
	st.WindowCount++
	if at > st.LastSeen {
		st.LastSeen = at
	}
	if err := repo.SaveState(ctx, src, en, st); err != nil {
		t.Fatalf("volume save for fold: %v", err)
	}
}

// sortedEdges returns the observed edges in a deterministic order, since map
// iteration order would otherwise vary between runs.
func sortedEdges(set map[coEdge]struct{}) []coEdge {
	out := make([]coEdge, 0, len(set))
	for e := range set {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.a.Field != b.a.Field {
			return a.a.Field < b.a.Field
		}
		if a.a.Value != b.a.Value {
			return a.a.Value < b.a.Value
		}
		if a.b.Field != b.b.Field {
			return a.b.Field < b.b.Field
		}
		return a.b.Value < b.b.Value
	})
	return out
}

// sortedNodes returns the observed nodes in a deterministic order.
func sortedNodes(set map[cooccurrence.NodeID]struct{}) []cooccurrence.NodeID {
	out := make([]cooccurrence.NodeID, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return out[i].Field < out[j].Field
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// TestTimingStateRoundTrip saves a 23-moment state (H = 11, 2H + 1 floats, §7.2) and
// requires the read-back to be bit-identical, including after an overwrite; an absent
// entity reports ok = false (§7.5).
func TestTimingStateRoundTrip(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	repo := store.Timing()
	src, en := event.SourceID("it-timing"), event.EntityID("acct-roundtrip")

	const order = 11
	primes := []float64{2, 3, 5, 7, 11, 13, 17, 19, 23, 29, 31}
	st := &timing.State{Moments: timing.NewMoments(order), LastSeen: 987_654_321_012_345}
	for i := 0; i < order; i++ {
		sign := 1.0
		if i%2 == 1 {
			sign = -1
		}
		st.Moments.C[i] = sign * math.Sqrt(primes[i])
		st.Moments.S[i] = 1 / (primes[i] * 1e15) // tiny magnitudes must survive verbatim
	}
	st.Moments.W = math.Pi * 1e3
	if got := st.Moments.Size(); got != 23 {
		t.Fatalf("stored floats = %d, want 23", got)
	}

	if err := repo.SaveState(ctx, src, en, st); err != nil {
		t.Fatalf("save: %v", err)
	}
	requireTimingBits(t, ctx, repo, src, en, st)

	// Overwrite through the ON CONFLICT path and read back again.
	st.Moments.Observe(timing.PhaseOfTimestamp(st.LastSeen), 0.5)
	st.LastSeen += 3 * event.Hour
	if err := repo.SaveState(ctx, src, en, st); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	requireTimingBits(t, ctx, repo, src, en, st)

	_, ok, err := repo.FindByEntity(ctx, src, event.EntityID("never-seen"))
	if err != nil {
		t.Fatalf("absent entity: %v", err)
	}
	if ok {
		t.Fatal("absent entity must report ok = false (§7.5 cold start)")
	}
}

// requireTimingBits reads the entity's state back and asserts bit-identity with want.
func requireTimingBits(t *testing.T, ctx context.Context, repo timing.StateRepository, src event.SourceID, en event.EntityID, want *timing.State) {
	t.Helper()
	got, ok, err := repo.FindByEntity(ctx, src, en)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !ok {
		t.Fatal("saved entity must be found")
	}
	if got.Moments.H() != want.Moments.H() {
		t.Fatalf("H = %d, want %d", got.Moments.H(), want.Moments.H())
	}
	mustSameBits(t, "W", want.Moments.W, got.Moments.W)
	for h := 0; h < want.Moments.H(); h++ {
		mustSameBits(t, fmt.Sprintf("C[%d]", h), want.Moments.C[h], got.Moments.C[h])
		mustSameBits(t, fmt.Sprintf("S[%d]", h), want.Moments.S[h], got.Moments.S[h])
	}
	if got.LastSeen != want.LastSeen {
		t.Fatalf("last seen = %d, want %d", got.LastSeen, want.LastSeen)
	}
}

// TestMigrationIdempotence opens the store twice against the same schema: the
// embedded CREATE TABLE IF NOT EXISTS migrations must apply cleanly both times.
func TestMigrationIdempotence(t *testing.T) {
	first := newStore(t) // first New: migrations create the tables
	second, err := postgres.New(context.Background(), itConnString, testHalfLife)
	if err != nil {
		t.Fatalf("second New on the same schema: %v", err)
	}
	second.Close()
	_ = first // closed by newStore's cleanup
}

// TestFindAllByEntityFieldOrdersByValue inserts values in shuffled order and requires
// the read-back in ascending byte-wise order (README trap 5: every query feeding
// scoring carries an explicit total ORDER BY, under the C locale). The mixed-case
// values are the canary: a locale-aware collation would order "a" before "B".
func TestFindAllByEntityFieldOrdersByValue(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	repo := store.Novelty()
	src, en, f := event.SourceID("it-order"), event.EntityID("acct-order"), event.FieldPath("auth.type")

	shuffled := []string{"kerberos", "B", "z-last", "0-first", "_score", "NTLM", "a", "Z", "b", "ntlm"}
	base := event.Timestamp(42) * event.Hour
	for i, v := range shuffled {
		at := base + event.Timestamp(i)*event.Minute
		if err := repo.SaveObservation(ctx, src, en, f, v, at); err != nil {
			t.Fatalf("save %q: %v", v, err)
		}
	}

	rows, err := repo.FindAllByEntityField(ctx, src, en, f, base+event.Timestamp(len(shuffled))*event.Minute)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if len(rows) != len(shuffled) {
		t.Fatalf("rows = %d, want %d", len(rows), len(shuffled))
	}
	want := append([]string(nil), shuffled...)
	sort.Strings(want) // Go's byte-wise order, the repository contract's total order
	for i, r := range rows {
		if r.Value != want[i] {
			t.Fatalf("row %d = %q, want %q (ascending byte-wise value order)", i, r.Value, want[i])
		}
	}
}
