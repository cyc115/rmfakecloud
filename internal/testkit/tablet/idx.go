package tablet

const digits = "0123456789abcdefghijklmnopqrstuvwxyz"

// Between returns a key strictly between a and b in string order. a == ""
// means no lower bound, b == "" no upper bound. Keys never end in "0", so a
// key can always be inserted before any other. Nothing sorts below "0", so
// Between("", "0") has no valid answer and returns "0".
func Between(a, b string) string {
	if b == "0" {
		return b
	}
	if b != "" {
		n := 0
		for n < len(b) && digitAt(a, n) == digitAt(b, n) {
			n++
		}
		if n > 0 {
			// a is zero-padded: past its end it is the empty (minimum) key
			rest := ""
			if n < len(a) {
				rest = a[n:]
			}
			return b[:n] + Between(rest, b[n:])
		}
	}
	da := 0
	if a != "" {
		da = digitAt(a, 0)
	}
	db := len(digits)
	if b != "" {
		db = digitAt(b, 0)
	}
	if db-da > 1 {
		return string(digits[(da+db+1)/2])
	}
	if len(b) > 1 {
		return b[:1]
	}
	rest := ""
	if len(a) > 1 {
		rest = a[1:]
	}
	return string(digits[da]) + Between(rest, "")
}

func digitAt(s string, i int) int {
	if i >= len(s) {
		return 0
	}
	for j := 0; j < len(digits); j++ {
		if digits[j] == s[i] {
			return j
		}
	}
	return 0
}
