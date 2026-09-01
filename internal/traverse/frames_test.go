package traverse

import "testing"

func TestExtractFramesPython(t *testing.T) {
	// Python prints the deepest frame last; ExtractFrames must normalise to
	// innermost-first so index 0 is always the throw site.
	body := `Traceback (most recent call last):
  File "/srv/app/api/routes.py", line 88, in handle_refund
    return process(order)
  File "/srv/app/billing/refund.py", line 42, in process
    amount = order.customer.balance
AttributeError: 'NoneType' object has no attribute 'balance'`

	frames := ExtractFrames(body)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2: %+v", len(frames), frames)
	}
	if frames[0].Path != "/srv/app/billing/refund.py" || frames[0].Line != 42 || frames[0].Func != "process" {
		t.Errorf("throw site = %+v, want billing/refund.py:42 in process", frames[0])
	}
	if frames[1].Func != "handle_refund" {
		t.Errorf("caller frame = %+v, want handle_refund", frames[1])
	}
}

func TestExtractFramesNode(t *testing.T) {
	body := `TypeError: Cannot read properties of undefined (reading 'id')
    at chargeCard (/app/src/billing/charge.js:57:22)
    at async processOrder (/app/src/orders/process.js:19:5)`

	frames := ExtractFrames(body)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2: %+v", len(frames), frames)
	}
	if frames[0].Path != "/app/src/billing/charge.js" || frames[0].Line != 57 {
		t.Errorf("frame 0 = %+v", frames[0])
	}
	if frames[0].Func != "chargeCard" {
		t.Errorf("frame 0 func = %q, want chargeCard", frames[0].Func)
	}
}

func TestExtractFramesJava(t *testing.T) {
	body := `java.lang.NullPointerException
	at com.acme.billing.Refunder.refund(Refunder.java:112)
	at com.acme.api.Controller.post(Controller.java:44)`

	frames := ExtractFrames(body)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2: %+v", len(frames), frames)
	}
	if frames[0].Path != "Refunder.java" || frames[0].Line != 112 || frames[0].Func != "refund" {
		t.Errorf("frame 0 = %+v", frames[0])
	}
}

func TestExtractFramesGoPanicPicksUpFunctionName(t *testing.T) {
	body := `panic: runtime error: invalid memory address
goroutine 42 [running]:
main.(*Server).handleRefund(0xc000112233)
	/build/api/server.go:210 +0x1c`

	frames := ExtractFrames(body)
	if len(frames) == 0 {
		t.Fatal("no frames extracted")
	}
	if frames[0].Path != "/build/api/server.go" || frames[0].Line != 210 {
		t.Fatalf("frame 0 = %+v", frames[0])
	}
	// The function name lives on the line above the file:line in a Go panic.
	if frames[0].Func != "handleRefund" {
		t.Errorf("frame 0 func = %q, want handleRefund", frames[0].Func)
	}
}

func TestExtractFramesDeduplicatesRecursion(t *testing.T) {
	body := `RecursionError: maximum recursion depth exceeded
    at walk (/app/tree.js:10:3)
    at walk (/app/tree.js:10:3)
    at walk (/app/tree.js:10:3)
    at main (/app/index.js:4:1)`

	frames := ExtractFrames(body)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2 after de-duplication: %+v", len(frames), frames)
	}
}

func TestExtractFramesIgnoresNonSourceNumbers(t *testing.T) {
	// Version strings, IPs, and durations must not be mistaken for frames.
	body := `Connection to 10.0.1.44:5432 timed out after 30000ms (client v2.14.1)`
	if frames := ExtractFrames(body); len(frames) != 0 {
		t.Errorf("got %d frames from a frame-less alert: %+v", len(frames), frames)
	}
}

func TestRankFramesResolvesBuildPrefixes(t *testing.T) {
	// The runtime reports "/srv/app/billing/refund.py"; the index stores
	// "billing/refund.py". The longest unambiguous suffix must bridge the two.
	frames := []Frame{
		{Path: "/srv/app/billing/refund.py", Line: 42},
		{Path: "/srv/app/api/routes.py", Line: 88},
	}
	repoPaths := []string{"billing/refund.py", "api/routes.py", "billing/charge.py"}

	rank := RankFrames(frames, repoPaths)

	if rank.Empty() {
		t.Fatal("no frames resolved")
	}
	if got := rank.Paths(); len(got) != 2 || got[0] != "billing/refund.py" {
		t.Fatalf("Paths() = %v, want throw site first", got)
	}
	if s := rank.Score("billing/refund.py"); s != 1.0 {
		t.Errorf("throw-site score = %v, want 1.0", s)
	}
	if s := rank.Score("api/routes.py"); s != 0.5 {
		t.Errorf("second-frame score = %v, want 0.5", s)
	}
	if s := rank.Score("billing/charge.py"); s != 0 {
		t.Errorf("unrelated file score = %v, want 0", s)
	}
}

func TestRankFramesRefusesAmbiguousBasenames(t *testing.T) {
	// "index.js" exists in three packages. Matching on the bare basename would
	// pick one at random, so an unresolvable frame must resolve to nothing.
	frames := []Frame{{Path: "/build/dist/index.js", Line: 1}}
	repoPaths := []string{"a/index.js", "b/index.js", "c/index.js"}

	if rank := RankFrames(frames, repoPaths); !rank.Empty() {
		t.Errorf("ambiguous basename resolved to %v, want no match", rank.Paths())
	}
}

func TestRankFramesHandlesWindowsPaths(t *testing.T) {
	frames := []Frame{{Path: `C:\build\billing\refund.py`, Line: 42}}
	rank := RankFrames(frames, []string{"billing/refund.py"})
	if _, ok := rank.Position("billing/refund.py"); !ok {
		t.Error("windows-style path did not resolve")
	}
}
