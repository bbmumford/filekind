package filekind

import (
	"bufio"
	"io"
	"math"
)

func negZero() float64 { return math.Copysign(0, -1) }

func nan() float64 { return math.NaN() }

func bufioWriter(w io.Writer) *bufio.Writer { return bufio.NewWriter(w) }
