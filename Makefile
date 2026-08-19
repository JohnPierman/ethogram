# Build, test, and evidence-generation targets.
#
# The evidence targets are deliberately chained so that a figure cannot be produced
# without a backing result file: `figures` depends on `verify-provenance`, which fails
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
FIGURES   := $(ROOT)/docs/figures
REPORT    := $(ROOT)/docs/report.html
DASHBOARD := $(ROOT)/docs/dashboard.html
PAPER_MD   := $(ROOT)/docs/PAPER.md
PAPER_HTML := $(ROOT)/docs/paper.html
PAPER_PDF  := $(ROOT)/docs/paper.pdf

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

# ---------------------------------------------------------------------------
# Evidence
#
# Never put a number in a dashboard, table, or report that did not come out of a real
# run. These targets make that structurally hard rather than a matter of discipline.
# ---------------------------------------------------------------------------

# Fails if any figure or table lacks a backing results/*.json emitted by a real run,
# or if any result file is missing its provenance block.
.PHONY: verify-provenance
verify-provenance:
	$(GO) run ./cmd/report -verify-provenance -results $(RESULTS) -figures $(FIGURES)

# Regenerates every SVG and the static report from the committed results. A
# hypothesis with no result file renders as a literal NOT RUN card, never blank and
# never zero.
.PHONY: figures
figures: verify-provenance
	$(GO) run ./cmd/report -results $(RESULTS) -figures $(FIGURES) -out $(REPORT)
	@echo "wrote $(REPORT) and $(FIGURES)/*.svg"

.PHONY: report
report: figures

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

# The interactive dashboard. It reads the same results directory and embeds a distilled
# index, so a new run appears on it without any edit here.
#
# Deliberately NOT dependent on verify-provenance: that target checks figures against
# their backing runs, and the dashboard renders no figures. Its own guarantee is
# structural instead — it can only display what a result file recorded, and it renders a
# missing measurement as NOT RUN rather than as zero.
.PHONY: dashboard
dashboard:
	$(GO) run ./cmd/dashboard -results $(RESULTS) -out $(DASHBOARD)
	@echo "wrote $(DASHBOARD)"

# The same currency guarantee for the figures and the static report, which had none.
#
# The gap was not theoretical: running the renderer as a diagnostic silently rewrote four
# committed SVGs and produced two the repository had never held, which means the committed
# figures had been stale against the committed results with nothing anywhere saying so. A
# figure carries a run id in the manifest, so a reader takes it as evidence from that run;
# a stale one is evidence from a run that no longer exists.
.PHONY: figures-check
figures-check:
	$(GO) run ./cmd/report -check -results $(RESULTS) -figures $(FIGURES) -out $(REPORT)

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
# The page count is REPORTED rather than assumed. The target is 10-15 pages, exceeded only
# for figures and citations, and prose has a way of growing past a budget nobody measures.
# The print stylesheet runs about 480 words to the page; the screen one ran 330, which
# turned a short paper into a 55-page document.
.PHONY: paper
paper:
	$(GO) run ./cmd/thesis -in $(PAPER_MD) -out $(PAPER_HTML)
	@chrome=$$(command -v chrome || command -v google-chrome 	  || echo "/c/Program Files/Google/Chrome/Application/chrome.exe"); 	"$$chrome" --headless --disable-gpu --no-pdf-header-footer 	  --print-to-pdf="$(PAPER_PDF)" "file:///$(PAPER_HTML)" 2>/dev/null
	@python -c "import sys;b=open(sys.argv[1],'rb').read();n=b.count(b'/Type /Page')-b.count(b'/Type /Pages');w=len(open(sys.argv[2],encoding='utf-8').read().split());print('wrote %s: %d pages, %d words'%(sys.argv[1],n,w));sys.exit(1 if n>15 else 0)" 	  "$(PAPER_PDF)" "$(PAPER_MD)" 	  || { echo "OVER BUDGET: the paper ceiling is 15 pages"; exit 1; }

# The specification-versus-code document. Its §12 coverage table is MEASURED from a
# coverage profile at generation time rather than maintained by hand: the hand-written
# version drifted in both directions, overstating domain/calibration by 11.7 points and
# understating application by 25. Without a profile the document prints NOT MEASURED
# instead of a stale table, so this target produces one first.
.PHONY: implementation
implementation:
	$(GO) test ./domain/... ./application/... -coverprofile=$(COVER_PROFILE) -covermode=atomic -count=1
	$(GO) run ./cmd/partiii -coverage $(COVER_PROFILE) \
	  -out $(ROOT)/docs/part-iii.html -out-md $(ROOT)/docs/IMPLEMENTATION.md

# Chrome headless with absolute paths: a relative --screenshot path fails because the
# working directory resets.
.PHONY: screenshots
screenshots: figures
	@chrome=$$(command -v chrome || command -v google-chrome \
	  || echo "/c/Program Files/Google/Chrome/Application/chrome.exe"); \
	"$$chrome" --headless --disable-gpu --hide-scrollbars \
	  --force-device-scale-factor=2 --window-size=1600,1000 \
	  --screenshot="$(ROOT)/docs/figures/report.png" \
	  "file:///$(ROOT)/docs/report.html"
	@echo "wrote $(FIGURES)/report.png"

.PHONY: clean
clean:
	rm -f $(COVER_PROFILE)
	$(GO) clean -cache -testcache
