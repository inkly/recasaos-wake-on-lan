package web

import (
	"regexp"
	"strings"
	"testing"
)

// The page puts what the API says (a device's name above all) in with textContent alone, and
// loads nothing from anywhere else.
func TestThePageShowsDataAsText(t *testing.T) {
	for _, banned := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function", "setAttribute('on", "srcdoc"} {
		if strings.Contains(Page, banned) {
			t.Errorf("the page uses %s", banned)
		}
	}
	if regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?(https?:)?//`).MatchString(Page) {
		t.Error("the page loads something from another site")
	}
	if !strings.Contains(Page, ".textContent = device.name") {
		t.Error("the device's name is not set with textContent")
	}
}

// It signs in the way the dashboard does: the token the dashboard keeps, bare in Authorization.
func TestThePageUsesTheDashboardsToken(t *testing.T) {
	for _, want := range []string{"localStorage.getItem('access_token')", "Authorization: token", `href="/"`} {
		if !strings.Contains(Page, want) {
			t.Errorf("the page has no %s", want)
		}
	}
}
