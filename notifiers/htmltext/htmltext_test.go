package htmltext

import "testing"

func TestPlainText(t *testing.T) {
	in := "<h2><b>✅ Backup successful</b></h2>\n<i>date</i><br/><pre><code>$ restic &lt;x&gt; &amp; &#39;y&#39;</code></pre>"
	want := "✅ Backup successful\n\ndate\n$ restic <x> & 'y'"
	if got := PlainText(in); got != want {
		t.Errorf("PlainText:\n got %q\nwant %q", got, want)
	}
}
