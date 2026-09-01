package patch

import (
	"strings"
	"testing"
)

const refundPy = `def process(order):
    amount = order.customer.balance
    return charge(amount)

def refund(order):
    amount = order.customer.balance
    return credit(amount)
`

func TestApplyReplacesExactlyOnce(t *testing.T) {
	files := map[string]string{"billing/refund.py": refundPy}
	changes, err := Apply(files, []Edit{{
		Path: "billing/refund.py",
		Old:  "def process(order):\n    amount = order.customer.balance",
		New:  "def process(order):\n    if order.customer is None:\n        raise ValueError(\"order has no customer\")\n    amount = order.customer.balance",
		Why:  "guard against a missing customer",
	}})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want 1", len(changes))
	}
	c := changes[0]
	if !strings.Contains(c.After, "raise ValueError") {
		t.Error("replacement not present in result")
	}
	// The refund function shares the vulnerable line; it must be untouched.
	if strings.Count(c.After, "order.customer.balance") != 2 {
		t.Error("the edit changed more than its anchor")
	}
	if c.Additions != 2 || c.Deletions != 0 {
		t.Errorf("diff stat = +%d/-%d, want +2/-0", c.Additions, c.Deletions)
	}
}

func TestApplyRefusesAmbiguousAnchor(t *testing.T) {
	// "amount = order.customer.balance" appears in both functions. Guessing which
	// one to change would be a coin flip over production code.
	files := map[string]string{"billing/refund.py": refundPy}
	_, err := Apply(files, []Edit{{
		Path: "billing/refund.py",
		Old:  "    amount = order.customer.balance",
		New:  "    amount = 0",
	}})
	if err == nil {
		t.Fatal("expected an error for an ambiguous anchor")
	}
	if !strings.Contains(err.Error(), "matches 2 times") {
		t.Errorf("error = %q, want it to name the ambiguity", err)
	}
}

func TestApplyRefusesMissingAnchor(t *testing.T) {
	files := map[string]string{"billing/refund.py": refundPy}
	_, err := Apply(files, []Edit{{
		Path: "billing/refund.py",
		Old:  "def settle(order):",
		New:  "def settle(order, retry=True):",
	}})
	if err == nil {
		t.Fatal("expected an error when the anchor is not in the file")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q, want it to say the anchor is missing", err)
	}
}

func TestApplyRefusesUnknownFile(t *testing.T) {
	_, err := Apply(map[string]string{"a.py": "x = 1\n"}, []Edit{{
		Path: "b.py", Old: "x = 1", New: "x = 2",
	}})
	if err == nil {
		t.Fatal("expected an error for a file outside the fix context")
	}
}

func TestApplyComposesMultipleEditsToOneFile(t *testing.T) {
	files := map[string]string{"billing/refund.py": refundPy}
	changes, err := Apply(files, []Edit{
		{Path: "billing/refund.py", Old: "return charge(amount)", New: "return charge(amount, retry=True)"},
		{Path: "billing/refund.py", Old: "return credit(amount)", New: "return credit(amount, retry=True)"},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("got %d changes, want both edits folded into one file", len(changes))
	}
	if changes[0].Edits != 2 {
		t.Errorf("edit count = %d, want 2", changes[0].Edits)
	}
	if changes[0].Additions != 2 || changes[0].Deletions != 2 {
		t.Errorf("diff stat = +%d/-%d, want +2/-2", changes[0].Additions, changes[0].Deletions)
	}
}

func TestApplyRefusesNoOpAndEmptyAnchor(t *testing.T) {
	files := map[string]string{"a.py": "x = 1\n"}
	if _, err := Apply(files, []Edit{{Path: "a.py", Old: "x = 1", New: "x = 1"}}); err == nil {
		t.Error("expected an error for a no-op edit")
	}
	if _, err := Apply(files, []Edit{{Path: "a.py", Old: "", New: "y = 2"}}); err == nil {
		t.Error("expected an error for an empty anchor")
	}
}

func TestApplyRejectsEmptyEditSet(t *testing.T) {
	if _, err := Apply(map[string]string{"a.py": "x = 1\n"}, nil); err == nil {
		t.Error("expected an error when there is nothing to apply")
	}
}

func TestDiffStatCountsMovedLinesOnce(t *testing.T) {
	// A line that survives the edit must not be counted as both added and removed.
	before := "a\nb\nc\n"
	after := "a\nB\nc\n"
	adds, dels := diffStat(before, after)
	if adds != 1 || dels != 1 {
		t.Errorf("diffStat = +%d/-%d, want +1/-1", adds, dels)
	}
}

func TestPathsDeduplicates(t *testing.T) {
	got := Paths([]Edit{{Path: "a.py"}, {Path: "b.py"}, {Path: "a.py"}})
	if len(got) != 2 || got[0] != "a.py" || got[1] != "b.py" {
		t.Errorf("Paths = %v, want [a.py b.py]", got)
	}
}
