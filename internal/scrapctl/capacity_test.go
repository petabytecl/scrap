package scrapctl

import "testing"

func TestSafeStringSliceCapacity(t *testing.T) {
	maxInt := int(^uint(0) >> 1)

	for _, tc := range []struct {
		name  string
		base  int
		extra int
		want  int
	}{
		{name: "adds_extra", base: 3, extra: kubectlGlobalFlagCapacity, want: 7},
		{name: "exact_max", base: maxInt - kubectlGlobalFlagCapacity, extra: kubectlGlobalFlagCapacity, want: maxInt},
		{name: "overflow", base: maxInt, extra: kubectlGlobalFlagCapacity, want: maxInt},
		{name: "no_extra", base: 3, extra: 0, want: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := safeStringSliceCapacity(tc.base, tc.extra); got != tc.want {
				t.Fatalf("safeStringSliceCapacity(%d, %d) = %d, want %d", tc.base, tc.extra, got, tc.want)
			}
		})
	}
}
