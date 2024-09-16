package runnable

import "context"

type StopFunc = func()

type Runnable interface {
	Start(context.Context) (StopFunc, error)
}
