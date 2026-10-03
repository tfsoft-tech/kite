package bench

import (
	"encoding/json"
	"io"
)

func writeJSON(w io.Writer, v any) { _ = json.NewEncoder(w).Encode(v) }
