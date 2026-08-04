package app

import (
	"context"
	"errors"
	"time"
)

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
		a.shutdownDeadline = time.Now().Add(grace)
		if a.Pool != nil {
			a.Pool.Shutdown()
		}
		if a.proxySettings != nil {
			a.proxySettings.Close()
		} else if a.proxy != nil {
			a.proxy.Close()
		}
		if a.cleanupCancel != nil {
			a.cleanupCancel()
		}
		if a.healthCancel != nil {
			// Stop the health checker so it isn't probing NVIDIA against a
			// closing DB; in-flight probe write attempts will get cancelled.
			a.healthCancel()
		}
		if a.Server != nil {
			a.Server.setShutdownGrace(grace)
			a.Server.setShutdownDeadline(a.shutdownDeadline)
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
	var shutdownErr error
	if a.Server != nil {
		shutdownErr = a.Server.Shutdown(context.Background())
	}
	if a.recorderCancel != nil {
		// Stop recording only after HTTP has drained; handlers may enqueue their
		// final request log while they are unwinding during Server.Shutdown.
		a.recorderCancel()
	}
	if a.shutdownTimer != nil {
		a.shutdownTimer.Stop()
	}
	if a.rootCancel != nil {
		a.rootCancel()
	}
	if a.nvidiaClient != nil {
		a.nvidiaClient.Close()
	}
	if a.cleanupDone != nil {
		<-a.cleanupDone
	}
	if a.recorderDone != nil {
		<-a.recorderDone
	}
	if a.healthDone != nil {
		<-a.healthDone
	}
	// Close the reader pool before the writer: readers hold WAL read locks that
	// would otherwise make the writer's final checkpoint contend.
	if a.dbReader != nil {
		shutdownErr = errors.Join(shutdownErr, a.dbReader.Close())
	}
	if a.db == nil {
		return shutdownErr
	}
	return errors.Join(shutdownErr, a.db.Close())
}
