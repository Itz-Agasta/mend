package shop

import "testing"

func TestOrderTotal(t *testing.T) {
	cases := []struct {
		unit, qty, want int64
	}{
		{unit: 1000, qty: 1, want: 1000},
		{unit: 1000, qty: 4, want: 4000},
		{unit: 1000, qty: 5, want: 4750},
		{unit: 333, qty: 6, want: 1898}, // 1998 * 95 / 100, truncated
	}
	for _, c := range cases {
		if got := orderTotal(c.unit, c.qty); got != c.want {
			t.Errorf("orderTotal(%d, %d) = %d, want %d", c.unit, c.qty, got, c.want)
		}
	}
}
