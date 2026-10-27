package blueprintregistry

import "strings"

// validVersion accepts SemVer release and prerelease versions without build
// metadata. Numeric prerelease identifiers cannot contain leading zeroes.
func validVersion(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	parts := versionPattern.FindStringSubmatch(value)
	if parts == nil {
		return false
	}
	if parts[4] == "" {
		return true
	}
	for _, part := range strings.Split(parts[4], ".") {
		if isDigits(part) && len(part) > 1 && part[0] == '0' {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func compareNumericString(a, b string) int {
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return strings.Compare(a, b)
}

// compareVersion orders two previously validated semantic versions. Comparing
// ASCII digit strings avoids integer overflow in very large version fields.
func compareVersion(a, b string) int {
	left := versionPattern.FindStringSubmatch(a)
	right := versionPattern.FindStringSubmatch(b)
	for i := 1; i <= 3; i++ {
		if order := compareNumericString(left[i], right[i]); order != 0 {
			return order
		}
	}
	if left[4] == "" && right[4] == "" {
		return 0
	}
	if left[4] == "" {
		return 1
	}
	if right[4] == "" {
		return -1
	}
	la := strings.Split(left[4], ".")
	lb := strings.Split(right[4], ".")
	for i := 0; i < len(la) && i < len(lb); i++ {
		an := isDigits(la[i])
		bn := isDigits(lb[i])
		if an && !bn {
			return -1
		}
		if !an && bn {
			return 1
		}
		order := strings.Compare(la[i], lb[i])
		if an {
			order = compareNumericString(la[i], lb[i])
		}
		if order != 0 {
			return order
		}
	}
	if len(la) < len(lb) {
		return -1
	}
	if len(la) > len(lb) {
		return 1
	}
	return 0
}
