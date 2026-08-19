// Package provenance records what a run consumed without recording where the machine that
// ran it keeps its files.
//
// A result file's job is to let a reader confirm which bytes produced a number. The
// corpus SHA-256 does that completely: it is verifiable by anyone holding the same file,
// and it is what the project's provenance guarantee actually rests on. An absolute path
// adds nothing to that — nobody else has `C:\Users\<somebody>\Documents\...` — while
// publishing the directory layout of the machine the research was done on.
//
// So paths are recorded in the form that identifies the file within the project and no
// further.
package provenance

import (
	"path"
	"path/filepath"
	"strings"
)

// dataRoot is the directory corpora live under, and the anchor a recorded path is cut back
// to. It is the same directory `.gitignore` excludes, so a path relative to it is
// meaningful to a reader who has obtained the corpora themselves.
const dataRoot = "data"

// RecordedPath reduces a filesystem path to the part identifying the file within the
// project, discarding any absolute prefix.
//
//	C:\Users\someone\Documents\proj\data\lanl\auth.txt.gz  ->  data/lanl/auth.txt.gz
//	/home/someone/proj/data/lanl/auth.txt.gz               ->  data/lanl/auth.txt.gz
//	data/lanl/auth.txt.gz                                  ->  data/lanl/auth.txt.gz
//	/tmp/scratch/verify.txt.gz                             ->  scratch/verify.txt.gz
//
// Separators are normalised to forward slashes so a result file reads the same whichever
// platform produced it, which also makes two runs of the same corpus on different machines
// comparable rather than superficially different.
//
// An empty path stays empty: a run that consumed no such file records nothing, rather than
// recording a plausible-looking placeholder.
func RecordedPath(p string) string {
	if p == "" {
		return ""
	}

	// Windows paths may arrive with either separator, and on non-Windows filepath does not
	// treat a backslash as one at all.
	unified := path.Clean(strings.ReplaceAll(filepath.ToSlash(p), `\`, "/"))

	// Cut back to the data root wherever it appears, which is the form a reader can act
	// on: it is where the corpora are expected to be.
	segments := strings.Split(unified, "/")
	for i, s := range segments {
		if s == dataRoot && i+1 < len(segments) {
			return strings.Join(segments[i:], "/")
		}
	}

	// Not under the data root: keep the last two segments, enough to identify the file
	// without naming the machine. A bare name keeps itself.
	if len(segments) >= 2 {
		return strings.Join(segments[len(segments)-2:], "/")
	}
	return unified
}
