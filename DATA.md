# Corpora

Dataset licences are separate from, and independent of, the licence covering this
source code. See LICENSE for the code; the terms below govern the data.

**No corpus data is committed to this repository.** `data/` is in `.gitignore`. Only
small result JSON files and generated figures are committed. Every figure and table
records the SHA-256 of the corpus files the run consumed, so a reader can confirm
which bytes produced a number without those bytes being in the history.

Row counts and event counts in this file were measured from the files themselves, not
quoted from the publishers' documentation. Where a publisher's stated figure and a
measured figure differ, both are recorded and the measured one is used.

---

## LANL — Comprehensive, Multi-Source Cyber-Security Events

Reference [48] of the whitepaper. Primary corpus: it is the only one of the three with
published baselines to compare against (§12.1, and [21], [49]–[52]).

- **Source:** <https://csr.lanl.gov/data/cyber1/>
- **Licence:** **CC0 1.0 Universal (public domain dedication).** Los Alamos National
  Laboratory has waived all copyright and related or neighbouring rights. Published
  from the United States.
- **Approvals recorded by the publisher:** LANL Human Subject Research Review Board
  approval LANL 14-07 X; public release approval LA-UR-15-23810.
- **Obtaining it:** the files are not at stable public URLs. The site requires an
  email address and a statement of intended use, and issues a time-limited token; the
  download path is then `/data-fence/<token>/cyber1/<file>`. A token was requested for
  this work with the stated usage "Academic evaluation of a calibrated behavioural
  anomaly-detection framework; reproducible benchmarking against published lateral
  movement baselines on public corpora."
- **De-identification, as the publisher describes it:** users, computers, processes
  and ports are de-identified as a unified set across all files, so `U1` denotes the
  same user in every file. `SYSTEM` and `Local Service` were deliberately *not*
  de-identified, nor were well-known ports. The specific timeframe is not disclosed.

### Citation

Required by the publisher for any publication using this data.

```bibtex
@InProceedings{akent-2015-enterprise-data,
  author    = {Alexander D. Kent},
  title     = {{Cybersecurity Data Sources for Dynamic Network Research}},
  year      = 2015,
  booktitle = {Dynamic Networks in Cybersecurity},
  month     = jun,
  publisher = {Imperial College Press}
}

@Misc{kent-2015-cyberdata1,
  author       = {Alexander D. Kent},
  title        = {{Comprehensive, Multi-Source Cyber-Security Events}},
  year         = {2015},
  howpublished = {Los Alamos National Laboratory},
  doi          = {10.17021/1179829}
}
```

### Schema, as documented by the publisher

Verified against the site before the parser was written. Fields are comma delimited,
and **a field with no valid value is a literal `?`**, which this framework represents
as present-but-unusable rather than absent, preserving the §5.3 distinction.

| File | Documented field list |
|---|---|
| `auth.txt.gz` | `time,source user@domain,destination user@domain,source computer,destination computer,authentication type,logon type,authentication orientation,success/failure` |
| `proc.txt.gz` | `time,user@domain,computer,process name,start/end` |
| `flows.txt.gz` | `time,duration,source computer,source port,destination computer,destination port,protocol,packet count,byte count` |
| `dns.txt.gz` | `time,source computer,computer resolved` |
| `redteam.txt.gz` | `time,user@domain,source computer,destination computer` |

Time is an integer count of seconds on the corpus's own epoch, **starting at 1**, over
58 consecutive days. The parser emits generic field paths and lets the registry of
§5.1 infer kinds; it does not hardcode the nine columns, which is what makes E6's
zero-code-change claim testable.

### Publisher-stated totals

Across all five files: 1,648,275,307 events, 12,425 users, 17,684 computers, 62,974
processes, approximately 12 GB compressed.

### Measured — files retrieved for this work

| File | Bytes (compressed) | SHA-256 |
|---|---:|---|
| `redteam.txt.gz` | 4,846 | `606635837c684ad11e464075ecf97bc5df325ff7d7f64614d2d8c8af18051669` |
| `auth.txt.gz` | 7,626,505,158 | `9c6b0cc261b0edd19324f6fd1839743224938a7f644ed202ca70bd70a89bf672` |
| `dns.txt.gz` | 185,104,940 | `3e1cb718baa6be7af7bd376324285be45d99ee2563d22bc4abf023572120cab9` |

### Measured — `redteam.txt` ground truth

Counted from the retrieved file.

| Quantity | Measured |
|---|---:|
| Rows | 749 |
| Distinct rows | 715 (34 exact duplicates) |
| Distinct users | 104 (all `U*`, i.e. human accounts) |
| Distinct source computers | 4 |
| Distinct destination computers | 301 |
| Distinct (user, src, dst) triples | 441 |
| First timestamp | 150,885 s (day 1.746) |
| Last timestamp | 2,557,047 s (day 29.595) |

`C17693` is the source computer for 701 of the 749 events (93.6%). Red-team activity
occupies days 1–29 of the 58, and 15 of those days carry none. The two largest days
are day 8 (273 events) and day 12 (209).

### A sampling artefact worth recording as a threat to validity

The publisher states that in the authentication data, **failed authentication events
are only included for users that had a successful authentication event somewhere in
the data set.** The failure population is therefore conditioned on eventual success,
which is not a property of a live telemetry stream. §12.5 commits to reporting threats
of this kind, and this one is reported alongside any result that uses the
success/failure field.

### Measured — `auth.txt` census

Counted from the retrieved file by streaming the full corpus (no publisher figures
quoted).

| Quantity | Measured |
|---|---:|
| Rows | 1,051,430,459 |
| Malformed rows | 0 |
| First timestamp | 1 s |
| Last timestamp | 5,011,199 s (day 57.999) |
| Span | 58.000 days exactly |
| Distinct source users (all account types) | 80,553 |
| Rows per day | 13.1M–25.0M |

A weekly rhythm is visible in the distinct-human-user counts (`U*` source accounts):
roughly 12,800 on weekdays against roughly 5,400 on weekends, identifying days 3–4 as
the first weekend. Daily row volume varies between 13.1M (day 19) and 25.0M (day 49).

### Burn-in and evaluation split — fixed before measurement

Every detector conditions on persisted history, so the first stretch of the corpus
warms state and is excluded from scoring. The split below was derived from the census
and the red-team timeline above, and is fixed here, in a commit made before any
end-to-end measurement. It is recorded in each result file as
`corpus.burn_in.fixed_at_commit` and is not retuned after seeing results.

- **Burn-in: t < 604,800 s (days 0–6).** One full week, including one weekend, so both
  weekly regimes inform the priors. Cost: the 49 red-team events on days 1, 2, 5 and 6
  are inside burn-in and never scored.
- **Scoring window: t ∈ [604,800, 5,011,200) (days 7–57).** Retains **700 of 749**
  red-team events (94.6%), including both major campaigns (day 8: 273 events, day 12:
  209).
- **Entity population: source users matching `U*@` (human accounts).** The unit of
  analysis is the individual: a verdict asks whether this person acted out of
  character against their own history. Machine accounts (`C*$@`), `SYSTEM`, and
  `ANONYMOUS LOGON` are not individuals and are excluded from the entity population.
  All 104 red-team users are `U*` accounts, so no ground truth is lost to this choice.
  Events from excluded accounts are not scored and do not update state; the report
  states this coverage restriction wherever results depend on it.

The binding constraint is that red-team activity begins at t = 150,885 s, day 1.746,
so burn-in cannot be long without discarding labels; one week costs 6.5% of them.

---

## CERT Insider Threat r5.2

Reference [53]. Second corpus.

- **Source:** the SEI "Insider Threat Test Dataset" collection on CMU KiltHub
  (figshare article 12841247), files `r5.2.tar.bz2` and `answers.tar.bz2`, both
  directly downloadable without a consent gate.
- **Licence and terms:** the collection is published by Carnegie Mellon University's
  Software Engineering Institute for research use; the distribution's own
  `SEI_Insider_README.txt` governs, and the terms are separate from this repository's
  code licence.
- **Synthetic provenance:** the corpus is generated, so §12.5's commitment applies —
  a detection may be an artefact of the generator rather than of behaviour, and any
  result on this corpus is reported with that caveat attached.

### Measured — files retrieved for this work

| File | Bytes | SHA-256 |
|---|---:|---|
| `r5.2.tar.bz2` | 11,137,370,937 | `9a7fadba71482474e8a2a7dddbf3620d450796d0059526db3362d41096d0f5bf` |
| `answers.tar.bz2` | 1,254,678 | recorded in the run that consumes it |

### Measured — the `logon` source

`r5.2/logon.csv` carries a header row and 1,810,070 data rows in the documented shape
`id,date,user,pc,activity`, for example:

```
{Q4D5-W4HH44UC-5188LWZK},01/02/2010 02:24:51,JBI1134,PC-0168,Logon
```

Two properties matter for this evaluation:

- **Time is a formatted timestamp**, `MM/DD/YYYY HH:MM:SS`, where LANL counts seconds
  from an arbitrary epoch. That is an encoding concern of the reader rather than a
  property of a source's fields, and admitting it required one generic reader
  capability. E6 reports that as a code change; see the E6 note below.
- **`id` is a per-event-unique token.** It is therefore a real-world instance of the
  field the §5.1 identifier guard exists for, and the §12.5 identifier control is
  exercised against it on genuine data rather than a synthetic field.

### The evaluation split, defined explicitly

§12.1 records the absence of a standard evaluation split for CERT as a threat to
comparability. This work therefore states its own rather than implying a standard:
`config/schemas/cert-r52-logon.json` is committed as data, the entity population is
the user column restricted to the `^[A-Z]{3}[0-9]{4}$` account pattern, event time is
measured from the epoch `2010-01-01T00:00:00Z` declared in that file, and the burn-in
is the first sixty days. The answer keys under `answers/r5.2-{1..4}` supply the four
scenarios' ground truth in the shape
`type,{id},MM/DD/YYYY HH:MM:SS,user,pc,detail`.

---

## DARPA OpTC

**Not run.** Stated plainly rather than half-run.

§12.1 lists OpTC third, after LANL and CERT, and this work treats it as the stretch
goal it was declared to be. It was not retrieved and no result on it exists, so no
figure, table or card in the report draws on it; the hypotheses it would have
contributed to are reported from the corpora that were run, and the report says which.

The reason is capacity rather than difficulty. The corpus is substantially larger than
LANL's, and the binding constraint on this evaluation was never obtaining data but
scoring it: a single pass over LANL's authentication log at the measured throughput
takes hours, and the frozen burn-in must be replayed before any event is scored. Adding
a third corpus would have bought breadth at the cost of the depth the ablations need,
and the ablations are what the framework's central design choices stand or fall on.

- **Licence and terms:** not recorded, because the corpus was not retrieved.
