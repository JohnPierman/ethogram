package provenance_test

import (
	"strings"
	"testing"

	"github.com/JohnPierman/ethogram/internal/provenance"
)

func TestRecordedPathCutsBackToTheDataRoot(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			"windows absolute",
			`C:\Users\someone\Documents\calibrated-anomaly-detection\data\lanl\auth.txt.gz`,
			"data/lanl/auth.txt.gz",
		},
		{
			"windows absolute, forward slashes",
			"C:/Users/someone/Documents/proj/data/lanl/redteam.txt.gz",
			"data/lanl/redteam.txt.gz",
		},
		{
			"unix absolute",
			"/home/someone/proj/data/cert/r5.2/logon.csv",
			"data/cert/r5.2/logon.csv",
		},
		{"already relative", "data/lanl/auth.txt.gz", "data/lanl/auth.txt.gz"},
		{"relative with dot", "./data/lanl/auth.txt.gz", "data/lanl/auth.txt.gz"},
	} {
		if got := provenance.RecordedPath(tc.in); got != tc.want {
			t.Errorf("%s: RecordedPath(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestRecordedPathNamesNoMachine is the property this exists for: whatever goes in, no
// user name, home directory or drive letter comes out.
func TestRecordedPathNamesNoMachine(t *testing.T) {
	for _, in := range []string{
		`C:\Users\someone\Documents\calibrated-anomaly-detection\data\lanl\auth.txt.gz`,
		`C:\Users\SOMEONE~1\AppData\Local\Temp\verify.txt.gz`,
		"/home/someone/work/private-notes/corpus.gz",
		"/Users/someone/Library/data/lanl/dns.txt.gz",
	} {
		got := provenance.RecordedPath(in)
		for _, leak := range []string{"Users", "someone", "SOMEONE~1", "someone", "home", "AppData", "C:"} {
			if strings.Contains(got, leak) {
				t.Errorf("RecordedPath(%q) = %q, which still contains %q", in, got, leak)
			}
		}
	}
}

// TestRecordedPathOutsideTheDataRootKeepsEnoughToIdentify: a temporary or scratch corpus is
// still worth naming, just not locating.
func TestRecordedPathOutsideTheDataRootKeepsEnoughToIdentify(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`C:\Users\SOMEONE~1\AppData\Local\Temp\verify.txt.gz`, "Temp/verify.txt.gz"},
		{"/tmp/scratch/verify-labels.txt.gz", "scratch/verify-labels.txt.gz"},
		{"corpus.gz", "corpus.gz"},
	} {
		if got := provenance.RecordedPath(tc.in); got != tc.want {
			t.Errorf("RecordedPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRecordedPathEmptyStaysEmpty: a run that consumed no such file records nothing rather
// than a plausible-looking placeholder.
func TestRecordedPathEmptyStaysEmpty(t *testing.T) {
	if got := provenance.RecordedPath(""); got != "" {
		t.Errorf("RecordedPath(\"\") = %q, want empty", got)
	}
}

// TestRecordedPathIsStableAcrossPlatforms: the same corpus recorded on Windows and on Linux
// must read identically, or two runs of one corpus look like runs of two.
func TestRecordedPathIsStableAcrossPlatforms(t *testing.T) {
	windows := provenance.RecordedPath(`D:\research\proj\data\lanl\auth.txt.gz`)
	unix := provenance.RecordedPath("/srv/research/proj/data/lanl/auth.txt.gz")
	if windows != unix {
		t.Errorf("platform-dependent: %q vs %q", windows, unix)
	}
}
