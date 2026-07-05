package cmd

import "testing"

func TestParsePins(t *testing.T) {
	pins, err := parsePins([]string{"checkout=0.0.400", "billing=1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	if pins["checkout"] != "0.0.400" || pins["billing"] != "1.2.3" {
		t.Errorf("pins = %v", pins)
	}
	if got, _ := parsePins(nil); got != nil {
		t.Errorf("no flags should yield a nil map, got %v", got)
	}
	for _, bad := range []string{"checkout", "=1.0.0", "checkout="} {
		if _, err := parsePins([]string{bad}); err == nil {
			t.Errorf("parsePins(%q) should error", bad)
		}
	}
}
