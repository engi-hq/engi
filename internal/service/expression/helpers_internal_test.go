package expression

import (
	"time"

	"github.com/engi-hq/engi/internal/config"
)

type timeT = time.Time

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

func defaultCfgForTest() config.ExpressionConfig { return config.Default().Expression }
