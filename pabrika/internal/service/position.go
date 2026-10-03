package service

// Position math for ordering tickets inside a column. Pure, no DB.
const (
	Gap    = 1024.0
	MinGap = 1e-6
)

// Bottom is the position after the last ticket of a column (positions in any order); Gap if empty.
func Bottom(positions []float64) float64 {
	if len(positions) == 0 {
		return Gap
	}
	max := positions[0]
	for _, p := range positions[1:] {
		if p > max {
			max = p
		}
	}
	return max + Gap
}

// Top is the position before the first ticket of a column; Gap if empty. Negative values are allowed.
func Top(positions []float64) float64 {
	if len(positions) == 0 {
		return Gap
	}
	min := positions[0]
	for _, p := range positions[1:] {
		if p < min {
			min = p
		}
	}
	return min - Gap
}

// Between is the midpoint of a and b; renumber is true when the gap is below MinGap
// (including equal or inverted positions), meaning the column must be renumbered first.
func Between(a, b float64) (pos float64, renumber bool) {
	if b-a < MinGap {
		return (a + b) / 2, true
	}
	return (a + b) / 2, false
}

// PlanMove picks the position for inserting at index idx (0..len) into a column whose sorted
// positions, without the moved ticket, are neighbours. renumber=true means the caller must
// renumber the column (Renumber) and plan again.
func PlanMove(neighbours []float64, idx int) (pos float64, renumber bool) {
	n := len(neighbours)
	switch {
	case n == 0:
		return Gap, false
	case idx <= 0:
		return neighbours[0] - Gap, false
	case idx >= n:
		return neighbours[n-1] + Gap, false
	}
	return Between(neighbours[idx-1], neighbours[idx])
}

// Renumber returns the fresh positions Gap*i for i = 1..n.
func Renumber(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = Gap * float64(i+1)
	}
	return out
}
