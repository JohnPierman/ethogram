"""Check the paper against its length budget.

Two quantities, with different standing, and the split is the point of this file (#35).

The WORD budget is enforced everywhere. It is a property of the source alone, so it means
the same thing on every machine and can run in CI.

The PAGE count is enforced only on the platform whose renderer produced the committed PDF,
and reported everywhere else. It is not a property of the source: byte-identical source
measured 20 pages under Windows Chrome and 21 under macOS Chrome 141, because font embedding
differs and moves the line breaks that move the pages. A gate that fails on a machine that
did not build the artefact stops carrying information and starts being routed around, which
is what happened before this split existed.

Usage:
    python cmd/thesis/budget.py <paper.pdf> <paper.md> <pinned-platform>
"""

import re
import sys

# The allowance is 15 pages of body plus 5 for figures and citations. Nothing can tell those
# apart from outside, so 20 is the number, and the ceiling is deliberately tight: the
# constraint exists to prevent verbosity, not to be grown into.
MAX_PAGES = 20

# Measured on this stylesheet across six renders in one sitting: 470, 478, 504, 525, 532 and
# 565 words to the page, against the print stylesheet's own stated figure of about 480. The
# budget takes the low end rather than the mean, so the word gate binds slightly before the
# page gate would rather than after it -- a gate that trips second is decoration.
WORDS_PER_PAGE = 480
MAX_WORDS = MAX_PAGES * WORDS_PER_PAGE


def page_count(path):
    """Count pages in a PDF by its page objects.

    /Type /Pages is the tree node and /Type /Page the leaves, so the leaves are the
    difference. Tolerant of whitespace between the key and the value, which some producers
    vary.
    """
    with open(path, "rb") as fh:
        blob = fh.read()
    pages = len(re.findall(rb"/Type\s*/Page[^s]", blob))
    if pages:
        return pages
    # Fall back to the linearised /N if the objects are compressed.
    match = re.search(rb"/N\s+(\d+)", blob)
    return int(match.group(1)) if match else 0


def word_count(path):
    with open(path, encoding="utf-8") as fh:
        return len(fh.read().split())


def main(argv):
    if len(argv) != 4:
        print(__doc__.strip(), file=sys.stderr)
        return 2

    pdf, source, pinned = argv[1], argv[2], argv[3]
    pages, words = page_count(pdf), word_count(source)

    # Which platform is this? Only used to decide whether the page count is authoritative.
    if sys.platform.startswith("win"):
        here = "windows"
    elif sys.platform == "darwin":
        here = "macos"
    else:
        here = "linux"
    authoritative = here == pinned

    print(f"wrote {pdf}: {pages} pages, {words} words")

    failed = False

    if words > MAX_WORDS:
        print(
            f"OVER BUDGET: {words} words against a ceiling of {MAX_WORDS} "
            f"({MAX_PAGES} pages at {WORDS_PER_PAGE} words/page). This gate is a property "
            f"of the source and holds on every platform."
        )
        failed = True

    if pages > MAX_PAGES:
        if authoritative:
            print(
                f"OVER BUDGET: {pages} pages against a ceiling of {MAX_PAGES} "
                f"(15 body + 5 figures and citations), measured on {pinned}, which is the "
                f"platform that produces the committed PDF."
            )
            failed = True
        else:
            print(
                f"NOTE: {pages} pages against a ceiling of {MAX_PAGES}, but this is "
                f"{here} and the committed PDF is rendered on {pinned}. The page count is "
                f"renderer-dependent -- the same source measured 20 pages under Windows "
                f"Chrome and 21 under macOS Chrome -- so this is not enforced here and is "
                f"not evidence that the source is over budget. The word gate above is."
            )

    if not authoritative:
        print(
            f"NOTE: docs/paper.pdf rendered on {here}; the committed artefact is rendered "
            f"on {pinned}. Do not commit this PDF unless you are on {pinned}."
        )

    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
