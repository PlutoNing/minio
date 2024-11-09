// Copyright (c) 2015-2021 MinIO, Inc.
//
// This file is part of MinIO Object Storage stack
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <http://www.gnu.org/licenses/>.

package cmd

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
	"github.com/minio/minio/internal/logger"
)

/* 定义了一个带超时的关闭过程，用于关闭 globalMRFState。
该过程会尝试在特定时间（shutdownTimeout）内完成。如果
超时，则函数返回并结束等待。 */
func shutdownHealMRFWithTimeout() {
	const shutdownTimeout = time.Minute

	finished := make(chan struct{})
	go func() {
		globalMRFState.shutdown()
		close(finished)/* 当 shutdown() 完成时，finished 通道会被关闭，
		向主 Goroutine 发送完成信号 */
	}()
	/* 通过 select 语句同时等待两个事件：shutdownTimeout
	到时，或 shutdown() 操作完成 */
	select {
	case <-time.After(shutdownTimeout):
	case <-finished:
	}
}

/*  */
func handleSignals() {
	// Custom exit function
	exit := func(success bool) {
		if globalLoggerOutput != nil {
			globalLoggerOutput.Close()
		}

		// If global profiler is set stop before we exit.
		globalProfilerMu.Lock()
		defer globalProfilerMu.Unlock()
		for _, p := range globalProfiler {
			p.Stop()
		}

		if success {
			os.Exit(0)
		}

		os.Exit(1)
	}
	/* 关闭minio进程 */
	stopProcess := func() bool {
		shutdownHealMRFWithTimeout() // this can take time sometimes, it needs to be executed
		// before stopping s3 operations

		// send signal to various go-routines that they need to quit.
		/* 取消使用这个ctx的过程 */
		cancelGlobalContext()
		/* 关闭http server */
		if httpServer := newHTTPServerFn(); httpServer != nil {
			if err := httpServer.Shutdown(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				shutdownLogIf(context.Background(), err)
			}
		}

		/* todo 2024年11月7日23:27:40 */
		if objAPI := newObjectLayerFn(); objAPI != nil {
			shutdownLogIf(context.Background(), objAPI.Shutdown(context.Background()))
		}

		/* web ui相关 */
		if globalBrowserEnabled {/*  */
			if srv := newConsoleServerFn(); srv != nil {
				shutdownLogIf(context.Background(), srv.Shutdown())
			}
		}

		if globalEventNotifier != nil {
			globalEventNotifier.RemoveAllBucketTargets()
		}

		return true
	}

	/* 处理信号的loop */
	for {
		select {
		/* httpserver停止 */
		case err := <-globalHTTPServerErrorCh:
			shutdownLogIf(context.Background(), err)
			exit(stopProcess())
		/* oss停止? */
		case osSignal := <-globalOSSignalCh:
			logger.Info("Exiting on signal: %s", strings.ToUpper(osSignal.String()))
			/* 这行代码的作用是向 systemd 发送一个通知，表示服务正在优雅地关闭，
			这样 systemd 就不会因为超时或强制关闭而中断服务。 */
			daemon.SdNotify(false, daemon.SdNotifyStopping)
			exit(stopProcess())
		case signal := <-globalServiceSignalCh:
			switch signal {
				/* 重启的信号 */
			case serviceRestart:
				/* 重启的处理过程 */
				logger.Info("Restarting on service signal")
				daemon.SdNotify(false, daemon.SdNotifyReloading)
				stop := stopProcess()
				rerr := restartProcess()
				if rerr == nil {
					daemon.SdNotify(false, daemon.SdNotifyReady)
				}
				shutdownLogIf(context.Background(), rerr)
				exit(stop && rerr == nil)
			case serviceStop:
				logger.Info("Stopping on service signal")
				daemon.SdNotify(false, daemon.SdNotifyStopping)
				exit(stopProcess())
			}
		}
	}
}
