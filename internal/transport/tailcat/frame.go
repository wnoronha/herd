package tailcat

import (
	"fmt"
	"math"
)

func uint32FrameLen(n int) (uint32, error) {
	if n < 0 || int64(n) > math.MaxUint32 {
		return 0, fmt.Errorf("frame length out of range: %d", n)
	}
	return uint32(n), nil
}

func uint16Port(port int) uint16 {
	if port <= 0 || port > math.MaxUint16 {
		return 7946
	}
	return uint16(port)
}
