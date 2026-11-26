package catalog

// PublicMaxQuantity returns an upper bound only when it is consistent with the seller's minimum.
func PublicMaxQuantity(minimum, maximum *int64) *int64 {
	if minimum != nil && maximum != nil && *maximum < *minimum {
		return nil
	}
	return maximum
}
