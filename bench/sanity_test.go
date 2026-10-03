package bench

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllRoutersMatchEverything(t *testing.T) {
	for _, f := range nops {
		h := f.load(githubAPI)
		for _, r := range githubAPI {
			req := httptest.NewRequest(r.method, paramRE.ReplaceAllString(r.path, "x$1"), nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Errorf("%s %s %s -> %d", f.name, r.method, r.path, w.Code)
			}
		}
	}
	t.Logf("%d routes OK on %d frameworks", len(githubAPI), len(nops))
}
