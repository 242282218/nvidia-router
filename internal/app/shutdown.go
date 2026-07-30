package app

import "time"

const defaultShutdownGrace = 60 * time.Second

func (a *App) beginShutdown(grace time.Duration) {
	a.shutdownOnce.Do(func() {
		a.shutting.Store(true)
		if grace <= 0 {
			grace = a.resolveShutdownGrace()
		}
		if grace <= 0 {
			grace = defaultShutdownGrace
		}
		a.shutdownGrace = grace
		if a.Pool != nil {
			a.Pool.Shutdown()
		}
		if a.cleanupCancel != nil {
			a.cleanupCancel()
		}
		if a.Server != nil {
			a.Server.setShutdownGrace(grace)
		}
		if a.rootCancel != nil {
			a.shutdownTimer = time.AfterFunc(grace, a.rootCancel)
		}
	})
}

func (a *App) resolveShutdownGrace() time.Duration {
	if a.RuntimeSettings == nil {
		return defaultShutdownGrace
	}
	return time.Duration(a.RuntimeSettings.Snapshot().ShutdownGraceMS) * time.Millisecond
}

func (a *App) finishShutdown() error {
	if a.shutdownTimer != nil {
		a.shutdownTimer.Stop()
	}
	if a.rootCancel != nil {
		a.rootCancel()
	}
	if a.cleanupDone != nil {
		<-a.cleanupDone
	}
	if a.db == nil {
		return nil
	}
	if err := a.db.Close(); err != nil {
		return err
	}
	return nil
}
