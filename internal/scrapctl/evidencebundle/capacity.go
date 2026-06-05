package evidencebundle

func safeStringSliceCapacity(base, extra int) int {
	if extra <= 0 {
		return base
	}
	maxInt := int(^uint(0) >> 1)
	if base > maxInt-extra {
		return base
	}
	return base + extra
}
