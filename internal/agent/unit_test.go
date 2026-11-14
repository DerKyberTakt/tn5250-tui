package agent

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DerKyberTakt/tn5250-tui/internal/tn5250"
)

func signonSnap() tn5250.Snapshot {
	const cols = 80
	return snapBuilder{
		rows: 24, cols: cols,
		lines: map[int]string{
			0: "   Sign On   ",
			2: "  Your user name:",
			3: "  Password (max. 128):",
			5: "  Selection or command",
			6: "  ===>",
		},
		fields: []tn5250.FieldInfo{
			fieldAt(cols, 2, 30, 10, nil),
			fieldAt(cols, 3, 30, 10, func(f *tn5250.FieldInfo) { f.Nondisplay = true }),
			fieldAt(cols, 6, 10, 60, nil),
			fieldAt(cols, 8, 2, 20, func(f *tn5250.FieldInfo) { f.Bypass = true }),
		},
		cursor: [2]int{2, 30},
	}.build()
}

func TestFormatScreen(t *testing.T) {
	snap := signonSnap()
	oia := tn5250.OIA{Connected: true, InputInhibited: "none", DeviceName: "QPADEV0001"}

	out, err := formatScreen(snap, oia, ScreenOpts{})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.Text, "\n")
	if len(lines) != 24 {
		t.Fatalf("blank rows must be kept: got %d lines", len(lines))
	}
	if lines[2] != "  Your user name:" || lines[1] != "" || lines[23] != "" {
		t.Errorf("rows are trimmed on the right only: %q %q %q", lines[1], lines[2], lines[23])
	}
	if out.Cursor != (Pos{Row: 3, Col: 31}) {
		t.Errorf("cursor must be 1-based: %+v", out.Cursor)
	}
	if out.Size != "24x80" || out.Seq != 7 || len(out.Signature) != 8 || out.OIA.Device != "QPADEV0001" {
		t.Errorf("header: %+v", out)
	}
	if len(out.Fields) != 4 || out.Fields[0].Index != 1 || out.Fields[0].Row != 3 || out.Fields[0].Col != 31 {
		t.Fatalf("fields: %+v", out.Fields)
	}
	if out.Fields[0].Label != "Your user name:" || !out.Fields[1].Nondisplay || out.Fields[1].Text != "" {
		t.Errorf("labels/hidden: %+v", out.Fields[:2])
	}

	// rows filter: only those rows, the fields that start in them, and the range echoed
	out, err = formatScreen(snap, oia, ScreenOpts{Rows: "3-4"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "  Your user name:\n  Password (max. 128):" || out.Rows != "3-4" {
		t.Errorf("rows filter: %q (%s)", out.Text, out.Rows)
	}
	if len(out.Fields) != 2 || out.Fields[1].Index != 2 {
		t.Errorf("fields in rows 3-4: %+v", out.Fields)
	}

	// fields_only drops the text
	out, _ = formatScreen(snap, oia, ScreenOpts{FieldsOnly: true})
	if out.Text != "" || len(out.Fields) != 4 {
		t.Errorf("fields_only: %q %d", out.Text, len(out.Fields))
	}

	// same screen, same signature; a change changes it
	a, _ := formatScreen(snap, oia, ScreenOpts{})
	b, _ := formatScreen(snap, oia, ScreenOpts{Rows: "1-2"})
	if a.Signature != b.Signature {
		t.Error("the signature is of the whole screen, whatever the rows filter")
	}
	snap2 := snapBuilder{rows: 24, cols: 80, lines: map[int]string{0: "other"}}.build()
	c, _ := formatScreen(snap2, oia, ScreenOpts{})
	if c.Signature == a.Signature {
		t.Error("different screens must differ in signature")
	}
}

func TestParseRows(t *testing.T) {
	tests := []struct {
		spec     string
		lo, hi   int
		wantFail bool
	}{
		{"", 1, 24, false},
		{"5", 5, 5, false},
		{"4-20", 4, 20, false},
		{"4-", 4, 24, false},
		{"-10", 1, 10, false},
		{"20-40", 20, 24, false},
		{"0", 0, 0, true},
		{"9-3", 0, 0, true},
		{"x", 0, 0, true},
		{"30", 0, 0, true},
	}
	for _, tc := range tests {
		lo, hi, err := parseRows(tc.spec, 24)
		if (err != nil) != tc.wantFail {
			t.Errorf("%q: err=%v", tc.spec, err)
			continue
		}
		if err == nil && (lo != tc.lo || hi != tc.hi) {
			t.Errorf("%q: got %d-%d want %d-%d", tc.spec, lo, hi, tc.lo, tc.hi)
		}
	}
}

func TestResolveKey(t *testing.T) {
	tests := []struct {
		in   string
		want tn5250.Action
		fail bool
	}{
		{"enter", tn5250.ActionEnter, false},
		{"ENTER", tn5250.ActionEnter, false},
		{" Return ", tn5250.ActionEnter, false},
		{"f3", tn5250.PF(3), false},
		{"F24", tn5250.PF(24), false},
		{"pf12", tn5250.PF(12), false},
		{"PF1", tn5250.PF(1), false},
		{"pageup", tn5250.ActionPageUp, false},
		{"rolldown", tn5250.ActionPageUp, false},
		{"PageDown", tn5250.ActionPageDown, false},
		{"rollup", tn5250.ActionPageDown, false},
		{"fieldexit", tn5250.ActionFieldExit, false},
		{"field exit", tn5250.ActionFieldExit, false},
		{"field+", tn5250.ActionFieldPlus, false},
		{"field-", tn5250.ActionFieldMinus, false},
		{"backtab", tn5250.ActionBacktab, false},
		{"sysreq", tn5250.ActionSysReq, false},
		{"eraseeof", tn5250.ActionEraseEOF, false},
		{"eraseInput", tn5250.ActionEraseInput, false},
		{"fieldmark", tn5250.ActionFieldMark, false},
		{"dup", tn5250.ActionDup, false},
		{"reset", tn5250.ActionReset, false},
		{"f25", "", true},
		{"f0", "", true},
		{"", "", true},
		{"banana", "", true},
	}
	for _, tc := range tests {
		got, err := ResolveKey(tc.in)
		if (err != nil) != tc.fail {
			t.Errorf("%q: err=%v", tc.in, err)
			continue
		}
		if err == nil && got != tc.want {
			t.Errorf("%q: got %q want %q", tc.in, got, tc.want)
		}
	}
	// every alias the tool promises resolves
	for _, k := range strings.Fields("enter f1 pf1 f24 pf24 pageup pagedown rollup rolldown tab backtab fieldexit field+ field- home attn sysreq reset clear help eraseeof eraseinput dup fieldmark") {
		if _, err := ResolveKey(k); err != nil {
			t.Errorf("promised key %q does not resolve: %v", k, err)
		}
	}
}

func TestPostsToHost(t *testing.T) {
	for k, want := range map[string]bool{
		"enter": true, "f3": true, "pagedown": true, "attn": true, "sysreq": true, "clear": true, "help": true,
		"tab": false, "backtab": false, "reset": false, "home": false, "eraseeof": false, "fieldexit": false,
	} {
		a, err := ResolveKey(k)
		if err != nil {
			t.Fatal(err)
		}
		if postsToHost(a) != want {
			t.Errorf("%s: postsToHost=%v want %v", k, !want, want)
		}
	}
}

func TestResolveField(t *testing.T) {
	views := fieldViews(signonSnap())
	tests := []struct {
		name    string
		ref     fieldRef
		want    int // 1-based index
		wantErr string
	}{
		{"index", fieldRef{Index: 2}, 2, ""},
		{"label exact, colon ignored", fieldRef{Label: "your user name"}, 1, ""},
		{"label with colon", fieldRef{Label: "Your user name:"}, 1, ""},
		{"label partial", fieldRef{Label: "password"}, 2, ""},
		{"label dots ignored", fieldRef{Label: "Password . . . :"}, 2, ""},
		{"label ===>", fieldRef{Label: "===>"}, 3, ""},
		{"label missing", fieldRef{Label: "zip code"}, 0, "no input field is labelled"},
		{"position", fieldRef{Row: 3, Col: 35}, 1, ""},
		{"position outside", fieldRef{Row: 10, Col: 1}, 0, "no field at row 10"},
		{"index past the table", fieldRef{Index: 9}, 0, "no field 9"},
		{"protected field", fieldRef{Index: 4}, 0, "protected"},
		{"nothing given", fieldRef{}, 0, "needs an index"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveField(views, tc.ref)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %v", tc.wantErr, err)
				}
				if tc.wantErr != "needs an index" && !strings.Contains(err.Error(), "input fields") && !strings.Contains(err.Error(), "no input fields") {
					t.Errorf("the error must name the field table: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Index != tc.want {
				t.Errorf("got field %d want %d", got.Index, tc.want)
			}
		})
	}
}

func TestResolveFieldAmbiguous(t *testing.T) {
	const cols = 80
	snap := snapBuilder{
		rows: 24, cols: cols,
		lines: map[int]string{
			2: "  Name:",
			3: "  Name (short):",
		},
		fields: []tn5250.FieldInfo{fieldAt(cols, 2, 20, 10, nil), fieldAt(cols, 3, 20, 10, nil)},
	}.build()
	views := fieldViews(snap)
	if f, err := resolveField(views, fieldRef{Label: "name"}); err != nil || f.Index != 1 {
		t.Errorf("an exact label beats a partial one: %v %+v", err, f)
	}
	_, err := resolveField(views, fieldRef{Label: "nam"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "Name (short):") {
		t.Errorf("ambiguous label must list the candidates: %v", err)
	}
}

func TestLabelStopsAtOtherField(t *testing.T) {
	const cols = 80
	snap := snapBuilder{
		rows: 24, cols: cols,
		lines:  map[int]string{2: "  From:"},
		fields: []tn5250.FieldInfo{fieldAt(cols, 2, 10, 5, nil), fieldAt(cols, 2, 20, 5, nil)},
	}.build()
	views := fieldViews(snap)
	if views[0].Label != "From:" {
		t.Errorf("first label %q", views[0].Label)
	}
	if views[1].Label != "" {
		t.Errorf("a field directly after another field has no label, got %q", views[1].Label)
	}
}

func TestFindText(t *testing.T) {
	snap := snapBuilder{rows: 24, cols: 80, lines: map[int]string{1: "Hello hello  ", 5: "say HELLO"}}.build()
	hits := findText(snap, "hello", false)
	if len(hits) != 3 || hits[0] != (Hit{Row: 2, Col: 1, Line: "Hello hello"}) || hits[1].Col != 7 || hits[2].Row != 6 || hits[2].Col != 5 {
		t.Errorf("case-insensitive hits: %+v", hits)
	}
	if hits := findText(snap, "hello", true); len(hits) != 1 || hits[0].Col != 7 {
		t.Errorf("case-sensitive hits: %+v", hits)
	}
	if hits := findText(snap, "absent", false); len(hits) != 0 || hits == nil {
		t.Errorf("no hits must be an empty list: %#v", hits)
	}
}

func TestGuardedVerbs(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"DLTF FILE(X)", "DLT"},
		{"dltlib x", "DLT"},
		{"  chglibl", "CHG"},
		{"CLRPFM X", "CLR"},
		{"RMVLIBLE X", "RMV"},
		{"CRTPF X", "CRT"},
		{"INZPFM X", "INZ"},
		{"DELETE FROM X", "DEL"},
		{"DROP TABLE X", "DROP"},
		{"UPDATE X SET", "UPDATE"},
		{"insert into x", "INSERT"},
		{"MERGE INTO x", "MERGE"},
		{"PWRDWNSYS *IMMED", "PWRDWNSYS"},
		{"ENDSBS QBATCH", "ENDSBS"},
		{"ENDSYS", "ENDSYS"},
		{"ENDTCP", "ENDTCP"},
		{"STRSBS X", "STRSBS"},
		{"SBMJOB CMD(X)", "SBMJOB"},
		{"CALL PGM(X)", "CALL"},
		{"RUNSQL 'x'", "RUNSQL"},
		{"ADDLIBLE X", "ADD"},
		{"?DLTF", "DLT"},
		{"QSYS/DLTF X", "DLT"},
		{"WRKACTJOB", ""},
		{"WRKOUTQ X", ""},
		{"DSPJOBLOG", ""},
		{"GO MAIN", ""},
		{"1", ""},
		{"", ""},
		{"ALEX", ""},
	}
	for _, tc := range tests {
		if got := guardedVerb(tc.text); got != tc.want {
			t.Errorf("guardedVerb(%q) = %q, want %q", tc.text, got, tc.want)
		}
	}
	err := checkReadOnly("dltf x")
	if err == nil || err.Error() != "read-only mode: DLT is blocked; ask the user to run it" {
		t.Errorf("message: %v", err)
	}
	if checkReadOnly("WRKACTJOB") != nil {
		t.Error("WRK is read-only and must not be blocked")
	}
}

func TestParseField(t *testing.T) {
	tests := []struct {
		in     any
		ref    fieldRef
		cursor bool
		fail   bool
	}{
		{nil, fieldRef{}, true, false},
		{"", fieldRef{}, true, false},
		{"Cursor", fieldRef{}, true, false},
		{float64(3), fieldRef{Index: 3}, false, false},
		{"2", fieldRef{Index: 2}, false, false},
		{"User", fieldRef{Label: "User"}, false, false},
		{float64(0), fieldRef{}, false, true},
		{float64(1.5), fieldRef{}, false, true},
		{true, fieldRef{}, false, true},
	}
	for _, tc := range tests {
		ref, cur, err := parseField(tc.in)
		if (err != nil) != tc.fail {
			t.Errorf("%v: err=%v", tc.in, err)
			continue
		}
		if err == nil && (ref != tc.ref || cur != tc.cursor) {
			t.Errorf("%v: got %+v %v", tc.in, ref, cur)
		}
	}
}

func TestRingKeepsLastLines(t *testing.T) {
	r := newRing(5)
	for i := 0; i < 12; i++ {
		r.Add(strings.Repeat("x", i))
	}
	last := r.Last(10)
	if len(last) != 5 {
		t.Fatalf("ring size: %d", len(last))
	}
	if !strings.HasSuffix(last[4], strings.Repeat("x", 11)) {
		t.Errorf("newest line last: %q", last[4])
	}
	if got := r.Last(2); len(got) != 2 {
		t.Errorf("Last(2): %d", len(got))
	}
	r.Add(strings.Repeat("y", 1000))
	if l := r.Last(1)[0]; len(l) > maxTraceLine+60 {
		t.Errorf("long lines are cut: %d", len(l))
	}
}

// ---- settle ----------------------------------------------------------------------------

// fakeSource is a settleSource driven by the test.
type fakeSource struct {
	mu        sync.Mutex
	seq       uint64
	inhibited string
	alive     bool
	n         *notifier
}

func newFakeSource() *fakeSource {
	return &fakeSource{inhibited: "system", alive: true, n: newNotifier()}
}

func (f *fakeSource) State() (uint64, string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seq, f.inhibited, f.alive
}

func (f *fakeSource) Changed() <-chan struct{} { return f.n.Changed() }

func (f *fakeSource) set(bump bool, inhibited string) {
	f.mu.Lock()
	if bump {
		f.seq++
	}
	f.inhibited = inhibited
	f.mu.Unlock()
	f.n.Bump()
}

func TestSettleWaitsForQuietAndUnlock(t *testing.T) {
	src := newFakeSource()
	const quiet = 120 * time.Millisecond
	go func() {
		// the host answers in bursts: three updates 40 ms apart, unlocking with the last
		time.Sleep(40 * time.Millisecond)
		src.set(true, "system")
		time.Sleep(40 * time.Millisecond)
		src.set(true, "system")
		time.Sleep(40 * time.Millisecond)
		src.set(true, "none")
	}()
	start := time.Now()
	res, err := settle(context.Background(), src, settleOpts{Quiet: quiet, Max: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Settled || res.Inhibited != "none" || res.Seq != 3 {
		t.Errorf("result %+v", res)
	}
	// 120 ms of burst, then at least 120 ms of quiet after the last change
	if el := time.Since(start); el < 120*time.Millisecond+quiet-20*time.Millisecond {
		t.Errorf("settled too early: %v", el)
	}
}

func TestSettleLockedKeyboardTimesOut(t *testing.T) {
	src := newFakeSource() // stays locked
	start := time.Now()
	res, err := settle(context.Background(), src, settleOpts{Quiet: 20 * time.Millisecond, Max: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if res.Settled || res.Inhibited != "system" {
		t.Errorf("result %+v", res)
	}
	if el := time.Since(start); el < 180*time.Millisecond || el > 2*time.Second {
		t.Errorf("elapsed %v", el)
	}
}

func TestSettleErrorStateEndsEarly(t *testing.T) {
	src := newFakeSource()
	src.set(true, "error")
	start := time.Now()
	res, _ := settle(context.Background(), src, settleOpts{Quiet: 30 * time.Millisecond, Max: 5 * time.Second})
	if res.Settled || res.Inhibited != "error" {
		t.Errorf("result %+v", res)
	}
	if time.Since(start) > time.Second {
		t.Error("an operator error never clears by itself: do not wait out the cap")
	}
}

func TestSettleClosedConnectionAndCancel(t *testing.T) {
	src := newFakeSource()
	go func() {
		time.Sleep(30 * time.Millisecond)
		src.mu.Lock()
		src.alive = false
		src.mu.Unlock()
		src.n.Bump()
	}()
	res, err := settle(context.Background(), src, settleOpts{Max: 5 * time.Second})
	if err != nil || !res.Closed || res.Settled {
		t.Errorf("closed: %+v %v", res, err)
	}

	src2 := newFakeSource()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := settle(ctx, src2, settleOpts{Max: 5 * time.Second}); err == nil {
		t.Error("a cancelled context must end settle with its error")
	}
}

func TestSettleReadyPredicate(t *testing.T) {
	src := newFakeSource()
	src.set(false, "none")
	var mu sync.Mutex
	ready := false
	go func() {
		time.Sleep(80 * time.Millisecond)
		mu.Lock()
		ready = true
		mu.Unlock()
		src.set(true, "none")
	}()
	res, err := settle(context.Background(), src, settleOpts{Quiet: 30 * time.Millisecond, Max: 5 * time.Second, Ready: func() bool {
		mu.Lock()
		defer mu.Unlock()
		return ready
	}})
	if err != nil || !res.Settled {
		t.Errorf("ready predicate: %+v %v", res, err)
	}
}

func TestWaitForSeesChange(t *testing.T) {
	src := newFakeSource()
	go func() {
		time.Sleep(40 * time.Millisecond)
		src.set(true, "none")
	}()
	ok, err := waitFor(context.Background(), src, 5*time.Second, func() bool { s, _, _ := src.State(); return s > 0 })
	if err != nil || !ok {
		t.Errorf("waitFor: %v %v", ok, err)
	}
	ok, _ = waitFor(context.Background(), src, 60*time.Millisecond, func() bool { return false })
	if ok {
		t.Error("a false predicate must time out")
	}
}

func TestNotifierSubscribe(t *testing.T) {
	n := newNotifier()
	c, cancel := n.Subscribe()
	n.Bump()
	n.Bump() // second signal collapses into the pending one
	select {
	case <-c:
	default:
		t.Fatal("subscriber got no signal")
	}
	select {
	case <-c:
		t.Error("signals of a slow reader collapse into one")
	default:
	}
	cancel()
	cancel() // idempotent
	n.Bump()
	select {
	case <-c:
		t.Error("a cancelled subscriber gets nothing")
	default:
	}
	if n.Seq() != 3 {
		t.Errorf("seq %d", n.Seq())
	}
}
