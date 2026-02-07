package index

import "testing"

func TestBuildFTSQuery(t *testing.T) {
	got := buildFTSQuery(`hello "world" /path:test`)
	want := `"hello"* AND "world"* AND "/path:test"*`
	if got != want {
		t.Fatalf("unexpected fts query\nwant: %s\ngot:  %s", want, got)
	}
}

func TestTokenizeSearchTerms(t *testing.T) {
	got := tokenizeSearchTerms(`  hello,   "world"   (test)  `)
	if len(got) != 3 || got[0] != "hello" || got[1] != "world" || got[2] != "test" {
		t.Fatalf("unexpected tokens: %#v", got)
	}
}
