package domain

import "time"

type Clock interface{ Now() time.Time }
