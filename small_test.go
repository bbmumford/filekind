package filekind

import (
	"bufio"
	"io"
	"math"
)

func negZero() float64 { return math.Copysign(0, -1) }

func nan() float64 { return math.NaN() }

func streamTo(w io.Writer) stream { return stream{bufio.NewWriter(w)} }
