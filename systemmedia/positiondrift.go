//go:build darwin || windows

package systemmedia

import "time"

const positionDriftTolerance = time.Second
