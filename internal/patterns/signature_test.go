package patterns

import (
	"strings"
	"testing"

	"quisac/internal/traverse"
)

// Two runs of the same bug: different request id, different timestamp,
// different order id, different line number after the file grew.
const runOne = `Traceback (most recent call last):
  File "/srv/app/billing/refund.py", line 42, in process
    amount = order.customer.balance
AttributeError: 'NoneType' object has no attribute 'balance' (request 8f2a1c94-5b3e-4a21-9d77-2e1f0c8b4a63 at 2026-03-01T09:14:22Z, order 88213)`

const runTwo = `Traceback (most recent call last):
  File "/srv/app/billing/refund.py", line 47, in process
    amount = order.customer.balance
AttributeError: 'NoneType' object has no attribute 'balance' (request 1a4b7d02-9e88-4c15-b3f0-77ac9d1e2b56 at 2026-04-19T22:03:51Z, order 91002)`

func sigOf(body string) Signature {
	return Analyze("Refund failing", body, "billing-api", traverse.ExtractFrames(body))
}

func TestSameBugFingerprintsIdentically(t *testing.T) {
	a, b := sigOf(runOne), sigOf(runTwo)
	if a.Fingerprint != b.Fingerprint {
		t.Errorf("same bug produced different fingerprints:\n  %s\n  %s", a.Text, b.Text)
	}
}

func TestFingerprintMasksVolatileValues(t *testing.T) {
	s := sigOf(runOne)
	for _, leaked := range []string{"8f2a1c94", "2026-03-01", "88213"} {
		if strings.Contains(s.Text, leaked) {
			t.Errorf("signature leaked volatile value %q: %s", leaked, s.Text)
		}
	}
	if !strings.Contains(s.Text, "attributeerror") {
		t.Errorf("signature lost the error class: %s", s.Text)
	}
	if !strings.Contains(s.Text, "billing/refund.py") {
		t.Errorf("signature lost the throw site: %s", s.Text)
	}
}

func TestFingerprintExcludesLineNumbers(t *testing.T) {
	// The same defect keeps its identity when unrelated code above it grows.
	if strings.Contains(sigOf(runOne).Text, "::42") {
		t.Error("signature pinned to a line number")
	}
}

func TestDifferentBugsFingerprintDifferently(t *testing.T) {
	other := `Traceback (most recent call last):
  File "/srv/app/billing/charge.py", line 12, in charge
    gateway.submit(payload)
ConnectionError: gateway refused connection`

	if sigOf(runOne).Fingerprint == sigOf(other).Fingerprint {
		t.Error("two unrelated failures share a fingerprint")
	}
}

func TestServiceSeparatesOtherwiseIdenticalErrors(t *testing.T) {
	frames := traverse.ExtractFrames(runOne)
	a := Analyze("Refund failing", runOne, "billing-api", frames)
	b := Analyze("Refund failing", runOne, "reporting-api", frames)
	if a.Fingerprint == b.Fingerprint {
		t.Error("the same error in two services shares a fingerprint")
	}
}

func TestAnalyzeWithoutStackTrace(t *testing.T) {
	// A threshold alert carries no trace; it must still fingerprint stably.
	body := "CPU utilisation 94% exceeded threshold 90% for 5m"
	a := Analyze("High CPU", body, "billing-api", nil)
	b := Analyze("High CPU", "CPU utilisation 97% exceeded threshold 90% for 5m", "billing-api", nil)

	if a.Fingerprint == "" {
		t.Fatal("no fingerprint produced for a trace-less alert")
	}
	if a.Fingerprint != b.Fingerprint {
		t.Errorf("percentages were not masked:\n  %s\n  %s", a.Text, b.Text)
	}
}

func TestSimilarity(t *testing.T) {
	a := []string{"attributeerror", "nonetype", "balance", "refund"}

	if got := Similarity(a, a); got != 1.0 {
		t.Errorf("identical token bags scored %v, want 1.0", got)
	}
	if got := Similarity(a, []string{"connectionerror", "gateway", "timeout", "charge"}); got != 0 {
		t.Errorf("disjoint token bags scored %v, want 0", got)
	}

	near := []string{"attributeerror", "nonetype", "balance", "refund", "customer"}
	if got := Similarity(a, near); got < 0.7 || got >= 1.0 {
		t.Errorf("near-identical bags scored %v, want a high but sub-1.0 overlap", got)
	}
	if got := Similarity(a, nil); got != 0 {
		t.Errorf("empty bag scored %v, want 0", got)
	}
}

func TestSimilarityIsSymmetric(t *testing.T) {
	a := []string{"a", "bb", "ccc", "dddd"}
	b := []string{"bb", "ccc", "eeee"}
	if Similarity(a, b) != Similarity(b, a) {
		t.Error("Similarity is not symmetric")
	}
}

func TestRealWorldRunsClearTheMatchThreshold(t *testing.T) {
	// The two runs above must be close enough that tier 1 would fire.
	if got := Similarity(sigOf(runOne).Tokens, sigOf(runTwo).Tokens); got < DefaultSimilarity {
		t.Errorf("similarity %v is below the %v match threshold", got, DefaultSimilarity)
	}
}
