package shop

// bulkQty is the order size from which the bulk discount applies.
const bulkQty = 5

// orderTotal prices an order line. It lives in Go rather than SQL on purpose:
// an order priced from a bad unit price can only be repaired by re-running
// this code, not by replaying the INSERT that stored its result.
func orderTotal(unitCents, qty int64) int64 {
	subtotal := unitCents * qty
	if qty >= bulkQty {
		return subtotal * 95 / 100
	}
	return subtotal
}
