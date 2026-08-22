# Build, test, and evidence-generation targets.
#
# The evidence targets are deliberately chained so that a figure cannot be produced
# the build if any figure or table lacks one. See the provenance rules in README.md.

SHELL := /bin/sh
GO    ?= go

# Absolute path to the repository root. Chrome's --screenshot resets the working
# directory, so every path handed to it must be absolute (see `screenshots`).
ROOT  := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

COVER_MIN     := 80
COVER_PROFILE := $(ROOT)/coverage.out

# The coverage gate applies to the domain and application layers. The scope is
# resolved from the packages that actually exist rather than written as a literal
# pattern: `go test ./application/...` exits non-zero while that layer is still empty,
# which would fail the gate for the wrong reason.
COVER_PATTERN := /(domain|application)($$|/)

RESULTS   := $(ROOT)/results
DASHBOARD := $(ROOT)/docs/dashboard.html
PAPER_MD   := $(ROOT)/docs/PAPER.md
PAPER_HTML := $(ROOT)/docs/paper.html
PAPER_PDF  := $(ROOT)/docs/paper.pdf

# python3 on macOS and most Linux distributions; `python` on Windows, where python3 is
# often a Store shim that is not the interpreter. Overridable.
PY ?= $(shell command -v python3 || command -v python)

# The platform whose renderer produced the committed docs/paper.pdf.
#
# The page count is a property of the renderer as well as the source: byte-identical source
# measured 20 pages under Windows Chrome and 21 under macOS Chrome 141, because font
# embedding differs and moves the line breaks that move the pages. So the page ceiling is
# ENFORCED only here and REPORTED everywhere, and the enforced gate everywhere is the word
# budget, which no renderer can move. See cmd/thesis/budget.py.
PAPER_PLATFORM ?= windows

# The synthetic-attack corpus. The corpus is not in the repository — it is 100 MB of LANL
# data — and in a git worktree it is not under the repository root either, so DATA is
# overridable and every path below derives from it: `make inject DATA=/path/to/lanl`.
DATA          ?= $(ROOT)/data/lanl
INJECT_SOURCE ?= $(DATA)/auth-holdout-r7-d0-14.txt.gz
INJECT_CORPUS ?= $(DATA)/auth-injected-r7-d0-14.txt.gz
INJECT_LABELS ?= $(DATA)/injected-labels-r7.txt.gz

# The taxonomy is a committed artefact and the dashboard is built from it, so regenerating it
# under a different run id makes `make dashboard-check` fail for a reason that has nothing to
# do with a measurement changing. The default therefore names the taxonomy that is committed;
# override it deliberately when planting a genuinely new set.
INJECT_TAXONOMY ?= $(RESULTS)/injection-r7-d0-14.taxonomy.json
INJECT_RUN_ID   ?= inject-r7-d0-14-002

.PHONY: all
all: fmt vet test

# ---------------------------------------------------------------------------
# Build and test
# ---------------------------------------------------------------------------

# Only the layers that exist are formatted; gofmt errors on a missing directory, and
# the layers arrive over several phases.
LAYERS = domain application infrastructure cmd
existing_layers = $(foreach d,$(LAYERS),$(wildcard $(d)))

.PHONY: fmt
fmt:
	gofmt -w $(existing_layers)
	@echo "formatted: $(existing_layers)"

.PHONY: fmt-check
fmt-check:
	@out=$$(gofmt -l $(existing_layers)); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@echo "gofmt clean: $(existing_layers)"

.PHONY: vet
vet:
	$(GO) vet ./...

.PHONY: build
build:
	$(GO) build ./...

# Unit tests must not require a corpus or a database. Corpus-dependent tests sit
# behind the `corpus` build tag and Postgres-dependent tests behind `integration`,
# so that CI, which has neither, still runs the whole domain suite.
.PHONY: test
test:
	$(GO) test ./... -race -count=1

.PHONY: test-integration
test-integration:
	$(GO) test ./... -race -count=1 -tags integration

.PHONY: test-corpus
test-corpus:
	$(GO) test ./... -count=1 -tags corpus

# E8 gates every other hypothesis, so it has its own target and runs first in CI.
.PHONY: e8
e8:
	$(GO) test ./domain/detector/ -run 'TestE8|TestR4' -race -count=1 -v

# The §12.5 negative controls: identifier, ablation monotonicity, and wraparound.
.PHONY: controls
controls:
	$(GO) test ./... -run 'TestControl' -race -count=1 -v

.PHONY: cover
cover:
	@pkgs=$$($(GO) list ./... | grep -E '$(COVER_PATTERN)' || true); \
	if [ -z "$$pkgs" ]; then \
	  echo "FAIL: no packages matched $(COVER_PATTERN); the gate would pass vacuously"; \
	  exit 1; \
	fi; \
	echo "coverage scope:"; echo "$$pkgs" | sed 's/^/  /'; \
	$(GO) test $$pkgs -race -count=1 -coverprofile=$(COVER_PROFILE) -covermode=atomic
	@$(GO) tool cover -func=$(COVER_PROFILE) | tail -1
	@pct=$$($(GO) tool cover -func=$(COVER_PROFILE) | tail -1 | awk '{print $$3}' | tr -d '%'); \
	echo "coverage $$pct% (gate $(COVER_MIN)%)"; \
	awk -v p="$$pct" -v m="$(COVER_MIN)" 'BEGIN { exit (p+0 >= m+0) ? 0 : 1 }' \
	  || { echo "FAIL: coverage $$pct% is below the $(COVER_MIN)% gate"; exit 1; }

.PHONY: lint
lint:
	@command -v golangci-lint >/dev/null 2>&1 \
	  || { echo "golangci-lint not installed; see .golangci.yml"; exit 1; }
	golangci-lint run ./...

# ---------------------------------------------------------------------------
# Database
# ---------------------------------------------------------------------------

.PHONY: db-up
db-up:
	docker compose up -d --wait postgres
	@echo "postgres listening on 127.0.0.1:55432 (project cad)"

.PHONY: db-down
db-down:
	docker compose down

.PHONY: db-reset
db-reset:
	docker compose down -v
	docker compose up -d --wait postgres

# The synthetic-attack corpus, for measuring detection per attack TYPE.
#
# The real campaign arrives as one uneven mix — on the matched d7-9 run, 194 of 262 labelled
# events are novel pairings while 100 are volume bursts nothing detects — so a per-type table
# built from it alone cannot separate "this detector cannot see this mechanism" from "the
# corpus barely contains this mechanism". Planting attacks of named kinds separates them.
#
# The taxonomy lands in results/ rather than beside the corpus on purpose: the dashboard is
# built from results/ alone and must render the same page in CI, which has no corpus. The
# corpus manifest stays beside the corpus, because that is where the replay's guard against
# sampling an already-sampled corpus looks for it.
# No shell guard on the source corpus: this Makefile is run on Windows as well, where the
# recipe shell is not /bin/sh and `test` does not exist. cmd/inject reports the missing corpus
# and names the DATA override itself, which works everywhere the tool does.
.PHONY: inject
inject:
	$(GO) run ./cmd/inject -auth $(INJECT_SOURCE) -out $(INJECT_CORPUS) \
	  -labels $(INJECT_LABELS) -taxonomy $(INJECT_TAXONOMY) -run-id $(INJECT_RUN_ID) \
	  -real-labels $(DATA)/redteam.txt.gz -per-type 8
	@echo "wrote $(INJECT_CORPUS), $(INJECT_LABELS) and $(INJECT_TAXONOMY)"

# ---------------------------------------------------------------------------
# The corpus: derive every input from the two files the archive actually ships
# ---------------------------------------------------------------------------
#
# These targets exist because their absence cost a day. Every recorded run cites its inputs
# by SHA-256, which proves WHICH file was read and says nothing about how to make it, and for
# one release the derivations lived only in a shell history: two `cmd/subset` invocations
# whose parameters survived solely inside the manifests of files that are not in the
# repository, and a combined label file that no target, no command and no document knew how to
# build. Reproducing a result on a second machine meant reading bytes off the first one.
#
# What the archive ships is `auth.txt.gz` and `redteam.txt.gz`. Everything else here is
# derived from those two, deterministically: the subset selector is FNV-1a of the entity
# identifier and the injector is seeded, so the same two inputs give the same outputs on any
# machine. `make corpus-check` is what turns "should" into "did".

RAW_AUTH        ?= $(DATA)/auth.txt.gz
RAW_REDTEAM     ?= $(DATA)/redteam.txt.gz
R11_CORPUS      ?= $(DATA)/auth-r11-d0-14.txt.gz
HOLDOUT_CORPUS  ?= $(DATA)/auth-holdout-r7-d0-14.txt.gz
COMBINED_LABELS ?= $(DATA)/labels-combined-r7.txt.gz
CORPUS_DIGESTS  ?= $(ROOT)/config/corpus-digests.txt

# Entity samples, not event samples: whole entities are kept or dropped so per-entity
# histories stay intact, and every labelled entity is kept regardless. Two residues of the
# same modulus share no unlabelled entity, which is what makes one a held-out evaluation of
# the other.
.PHONY: corpus-r11
corpus-r11:
	$(GO) run ./cmd/subset -auth $(RAW_AUTH) -redteam $(RAW_REDTEAM) -out $(R11_CORPUS) -entity-sample 16 -sample-residue 11 -maxseconds 1209600

.PHONY: corpus-holdout
corpus-holdout:
	$(GO) run ./cmd/subset -auth $(RAW_AUTH) -redteam $(RAW_REDTEAM) -out $(HOLDOUT_CORPUS) -entity-sample 16 -sample-residue 7 -maxseconds 1209600

# The injected corpus, and the combined labels a replay over it needs. -combined-labels is
# not optional in practice: the injected corpus is scored against both ground truths at once
# and a replay takes one -redteam argument.
#
# INJECT_TAXONOMY defaults to the COMMITTED taxonomy, which this rewrites. That is deliberate
# for a genuinely new planting and wrong for a reproduction, so a reproduction should point it
# at a scratch path and diff: `make inject INJECT_TAXONOMY=/tmp/tax.json`, then compare
# per_type, victim_type, events_injected, parameters, order and premise. The run block carries
# timestamps and always differs.
.PHONY: corpus-injected
corpus-injected:
	$(GO) run ./cmd/inject -auth $(INJECT_SOURCE) -out $(INJECT_CORPUS) -labels $(INJECT_LABELS) -combined-labels $(COMBINED_LABELS) -taxonomy $(INJECT_TAXONOMY) -run-id $(INJECT_RUN_ID) -real-labels $(RAW_REDTEAM) -per-type 8

# Everything, in dependency order. The two subset passes read 239M rows each and are the
# expensive part.
.PHONY: corpus
corpus: corpus-r11 corpus-holdout corpus-injected
	@echo "corpus derived; run 'make corpus-check' before any replay"

# Verify every derived input against the digests the recorded runs cite. Fails closed: a
# reproduction that starts from a different corpus is not a reproduction, and the cheapest
# place to learn that is before a two-hour replay rather than after it.
.PHONY: corpus-check
corpus-check:
	@$(GO) run ./cmd/corpuscheck -digests $(CORPUS_DIGESTS) -dir $(DATA)

# ---------------------------------------------------------------------------
# Reproducing a recorded run
# ---------------------------------------------------------------------------
#
# One target per recorded run, because a run launched by hand is a run nobody else can
# repeat. The flags below are the ones the result file records in its `parameters` block; if
# they drift apart, the result file is authoritative and this is the bug.

.PHONY: replay-r11
replay-r11:
	$(GO) run ./cmd/replay -auth $(R11_CORPUS) -redteam $(RAW_REDTEAM) -out $(RESULTS)/lanl-r11-b1000-conf-d7-14.json -run-id lanl-r11-b1000-weighted-d7-14-005 -topk 1000 -budgets 10,100,1000 -conformal -pairing -novelty-rate -weighted

.PHONY: replay-inj
replay-inj:
	$(GO) run ./cmd/replay -auth $(INJECT_CORPUS) -redteam $(COMBINED_LABELS) -out $(RESULTS)/lanl-inj-b1000-conf-d7-14.json -run-id lanl-inj-b1000-weighted-d7-14-005 -topk 1000 -budgets 10,100,1000 -conformal -pairing -novelty-rate -weighted

.PHONY: analyse-r11
analyse-r11:
	$(GO) run ./cmd/analyse -run $(RESULTS)/lanl-r11-b1000-conf-d7-14.json -out $(RESULTS)/analysis-r11-b1000-conf.json -run-id analysis-r11-b1000-conf-003 -budgets 10,100,1000 -value-ratio 10

# Section 1.1's figure: one line per detector through its own operating points, across the
# whole budget range.
#
# The framework run it reads is deliberately NOT in results/. Answering a budget of 10,000
# needs -topk 10000, and a result file that retains ten thousand alerts a day for every arm
# is about 46 MB -- an intermediate on the scale of the alert ledger, which this repository
# already keeps out of results/ for the same reason. What IS committed is the compact curve
# file this target derives, which carries every plotted point with its provenance, and the
# SVG the paper embeds.
#
# Regenerating the SVG changes docs/paper.html, so `make paper` must follow. `paper-check`
# is what catches forgetting.
CURVE_RUN       ?= /tmp/r11-curve-b10000.json
CURVE_BASELINES ?= $(RESULTS)/baselines-r11-d7-14-full.json
CURVE_ARMS      ?= novelty,composite
CURVE_SVG       ?= $(ROOT)/cmd/thesis/figures/budget-curve.svg
CURVE_TABLE     ?= $(ROOT)/cmd/thesis/figures/budget-table.html
# The two operating points the headline table reports. One budget makes the comparison look
# like a property of the methods when most of it is a property of what was affordable; see
# tableBudgets in cmd/budgetcurve for why these two.
CURVE_TABLE_BUDGETS ?= 100,1000

CURVE_JSON      ?= $(RESULTS)/budget-curve-r11-d7-14.json

.PHONY: figure-budget-curve
figure-budget-curve:
	$(GO) run ./cmd/budgetcurve -run $(CURVE_RUN) -baselines $(CURVE_BASELINES) \
	  -out $(CURVE_JSON) -svg $(CURVE_SVG) -table $(CURVE_TABLE) \
	  -table-budgets $(CURVE_TABLE_BUDGETS) \
	  -run-id budget-curve-r11-d7-14-001 -arms $(CURVE_ARMS)
	@echo "regenerated $(CURVE_SVG); run 'make paper' so the rendered page follows"

# Redraw the figure from the curve file, without the replay.
#
# The measurement and the drawing have very different lifetimes. CURVE_RUN is 46 MB of retained
# alerts and is not in the repository; CURVE_JSON, which carries every plotted point with its
# provenance, is. So revising HOW the figure is drawn -- which is what happens whenever it turns
# out to be hard to read -- must not require reproducing a two-hour replay first, because the
# only other way to move a line is to edit the committed SVG by hand, and that is how a figure
# stops matching the numbers it claims to plot.
#
# A test asserts that this path and `figure-budget-curve` draw byte-identical figures from the
# same measurement, so using it is not a shortcut with a caveat.
.PHONY: figure-budget-curve-redraw
figure-budget-curve-redraw:
	$(GO) run ./cmd/budgetcurve -curve $(CURVE_JSON) -svg $(CURVE_SVG) -table $(CURVE_TABLE) \
	  -table-budgets $(CURVE_TABLE_BUDGETS)
	@echo "redrew $(CURVE_SVG); run 'make paper' so the rendered page follows"

# The interactive dashboard. It reads the same results directory and embeds a distilled
# index, so a new run appears on it without any edit here.
#
# The dashboard's guarantee is structural: it can only display what a result file recorded,
# and it renders a missing measurement as NOT RUN rather than as zero.
.PHONY: dashboard
dashboard:
	$(GO) run ./cmd/dashboard -results $(RESULTS) -out $(DASHBOARD)
	@echo "wrote $(DASHBOARD)"

# Regenerating from unchanged results must produce an unchanged file, or a committed
# dashboard churns on every build and a diff stops meaning "a measurement changed".
.PHONY: dashboard-check
dashboard-check:
	$(GO) run ./cmd/dashboard -check -results $(RESULTS) -out $(DASHBOARD)

# Fails when docs/PAPER.md has moved and the rendered page has not. The page carries no
# timestamp, so an unchanged source reproduces it byte for byte. The gate exists because two
# copies of an argument kept in agreement by memory is the defect this repository has already
# fixed twice: a coverage table that drifted from go test -cover, and a detector list that
# claimed a composition the code had stopped using.
.PHONY: paper-check
paper-check:
	$(GO) run ./cmd/thesis -check -in $(PAPER_MD) -out $(PAPER_HTML)

# The paper: the project's presentable artefact, rendered and printed.
#
# The page count is REPORTED rather than assumed, because prose has a way of growing past a
# budget nobody measures. The print stylesheet runs about 480 words to the page; the screen
# one ran 330, which turned a short paper into a 55-page document.
#
# The allowance is 15 pages of body plus 5 for figures and citations, so the gate is 20
# total. It cannot tell the two apart -- it counts pages in the PDF -- so 20 is the number
# it enforces and the 15/5 split is the author's to keep. The ceiling is deliberately tight:
# the constraint exists to prevent verbosity, not to be grown into.
.PHONY: paper
paper:
	$(GO) run ./cmd/thesis -in $(PAPER_MD) -out $(PAPER_HTML)
	@chrome=$$(command -v chrome || command -v google-chrome || command -v chromium \
	  || command -v chromium-browser \
	  || ls "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" 2>/dev/null \
	  || ls "/c/Program Files/Google/Chrome/Application/chrome.exe" 2>/dev/null \
	  || ls "/mnt/c/Program Files/Google/Chrome/Application/chrome.exe" 2>/dev/null); \
	if [ -z "$$chrome" ]; then \
	  echo "no Chrome found: looked for chrome, google-chrome, chromium," \
	       "chromium-browser, the macOS bundle and the Windows install."; \
	  echo "docs/paper.html is written and current; only the PDF needs a browser."; \
	  exit 1; \
	fi; \
	"$$chrome" --headless --disable-gpu --no-pdf-header-footer \
	  --print-to-pdf="$(PAPER_PDF)" "file:///$(PAPER_HTML)" 2>/dev/null
	@$(PY) $(ROOT)/cmd/thesis/budget.py "$(PAPER_PDF)" "$(PAPER_MD)" "$(PAPER_PLATFORM)"

# The paper's headline comparison: one row per method, one column per attack type, at
# each of the alert budgets the run measured.
#
# Derived rather than typed. The table crosses fifteen methods with seven ground truths, and
# keeping a hundred cells correct by hand across a re-run is not a reasonable expectation --
# the numbers are read out of the recorded runs and pasted in. Three budgets makes that three
# hundred cells, which is why the set is emitted in one pass rather than one invocation per
# budget: three invocations could read three different files and nothing in the output would
# say so.
#
# One table reads at one budget and the comparison moves a great deal with it -- at 1000
# alerts a day five of six planted types are reached, at 100 exactly one is, at 10 none at
# all. A reader given only the widest budget would take the framework's reach for a property
# of the methods when it is mostly a property of what was affordable.
#
# INJ_RUN is the injected-corpus replay, the only run with per-type ground truth.
INJ_RUN ?= $(RESULTS)/lanl-inj-b1000-conf-d7-14.json
INJ_BASELINES ?= $(RESULTS)/baselines-injected-r7-d7-14.json
METHOD_BUDGETS ?= 10,100,1000

.PHONY: method-table
method-table:
	$(GO) run ./cmd/methodtable -run $(INJ_RUN) -baselines $(INJ_BASELINES) \
	  -taxonomy $(INJECT_TAXONOMY) -budgets $(METHOD_BUDGETS)

# The robust-allocation analysis: the same per-mechanism rectangle the method table
# renders, solved as a two-person zero-sum game.
#
# It reads only committed result files, so unlike every replay target it runs without the
# corpus. That is a property of the question rather than a convenience: allocation is about
# how to spend a budget across detectors whose performance is already measured, so the
# measurement is the input and re-scoring the corpus would answer a different question.
#
# The prior and the attacker costs are STATED, never fitted. A cost fitted to the same
# labels the allocation is scored against would make the equilibrium a restatement of the
# corpus, which is the defect section 5.5's oracle rows exist to bound.
MATRIX_JSON   ?= $(RESULTS)/method-matrix-inj-d7-14.json
ROBUST_JSON   ?= $(RESULTS)/robust-inj-d7-14.json
# The same analysis with a low-and-slow-capable baseline admitted into the strategy set. Two
# files rather than one because they answer different questions: the first is what this
# framework's own arms guarantee, with the mechanism none of them reaches excluded as
# unreachable; the second is what admitting an arm that does reach it costs, and its `lof` row
# comes from the hundredfold easier sampled problem of section 5.7 rather than from a matched
# comparison. Reporting only the second would lean the headline on a baseline; reporting only
# the first would hide that the excluded column is purchasable.
ROBUST_LOF_JSON ?= $(RESULTS)/robust-inj-lof-d7-14.json
ROBUST_BUDGET ?= 1000
ROBUST_ADMIT  ?= lof
ROBUST_PRIOR  ?= credential_spray=0.30,lateral_chain=0.12,off_hours=0.08,privilege_escalation=0.05,low_and_slow=0.10,account_takeover=0.30,real campaign=0.05
ROBUST_COST   ?= credential_spray=1,lateral_chain=3,off_hours=1,privilege_escalation=2,low_and_slow=12,account_takeover=4,real campaign=4

.PHONY: matrix
matrix:
	$(GO) run ./cmd/methodtable -run $(INJ_RUN) -baselines $(INJ_BASELINES) 	  -taxonomy $(INJECT_TAXONOMY) -budgets $(METHOD_BUDGETS) -matrix $(MATRIX_JSON) >/dev/null

.PHONY: robust
robust: matrix
	$(GO) run ./cmd/robust -matrix $(MATRIX_JSON) -budget $(ROBUST_BUDGET) 	  -prior "$(ROBUST_PRIOR)" -attacker-cost "$(ROBUST_COST)" 	  -out $(ROBUST_JSON) -run-id robust-inj-d7-14-002 >/dev/null
	$(GO) run ./cmd/robust -matrix $(MATRIX_JSON) -budget $(ROBUST_BUDGET) 	  -admit $(ROBUST_ADMIT) -prior "$(ROBUST_PRIOR)" -attacker-cost "$(ROBUST_COST)" 	  -out $(ROBUST_LOF_JSON) -run-id robust-inj-lof-d7-14-001 >/dev/null
	@echo "wrote $(ROBUST_JSON) and $(ROBUST_LOF_JSON)"

.PHONY: clean
clean:
	rm -f $(COVER_PROFILE)
	$(GO) clean -cache -testcache
