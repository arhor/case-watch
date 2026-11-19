package models

import "time"

type Clock interface{ Now() time.Time }
