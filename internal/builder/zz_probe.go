package builder

import "fmt"

func zzProbe() {
	var w interface{ Write([]byte) (int, error) }
	fmt.Fprintf(w, "unchecked")
}
