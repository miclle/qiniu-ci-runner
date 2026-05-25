package github

import "testing"

func TestNextLinkParsesRelNext(t *testing.T) {
	header := `<https://api.github.com/repos/octo/repo/actions/runners?page=2>; rel="next", <https://api.github.com/repos/octo/repo/actions/runners?page=3>; rel="last"`
	got := nextLink(header)
	want := "https://api.github.com/repos/octo/repo/actions/runners?page=2"
	if got != want {
		t.Errorf("nextLink: got %q, want %q", got, want)
	}
}

func TestNextLinkReturnsEmptyWhenNoNext(t *testing.T) {
	header := `<https://api.github.com/repos/octo/repo/actions/runners?page=3>; rel="last"`
	if got := nextLink(header); got != "" {
		t.Errorf("nextLink (no next): got %q, want empty", got)
	}
}

func TestNextLinkReturnsEmptyForEmptyHeader(t *testing.T) {
	if got := nextLink(""); got != "" {
		t.Errorf("nextLink (empty header): got %q, want empty", got)
	}
}

func TestNextLinkIgnoresRelPrevOnly(t *testing.T) {
	header := `<https://api.github.com/repos/octo/repo/actions/runners?page=1>; rel="prev"`
	if got := nextLink(header); got != "" {
		t.Errorf("nextLink (prev only): got %q, want empty", got)
	}
}

func TestNextLinkHandlesOnlyNextLink(t *testing.T) {
	header := `<https://api.github.com/items?page=5>; rel="next"`
	want := "https://api.github.com/items?page=5"
	if got := nextLink(header); got != want {
		t.Errorf("nextLink (only next): got %q, want %q", got, want)
	}
}
