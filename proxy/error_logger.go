package proxy

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// errorFileLogger 将 400 错误详情写入日志文件，供后续分析优化
var errorFileLogger struct {
	once   sync.Once
	logger *log.Logger
	file   *os.File
}

// initErrorLogger 初始化错误日志文件（logs/bad_request.log）
func initErrorLogger() *log.Logger {
	errorFileLogger.once.Do(func() {
		dir := "logs"
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("创建错误日志目录失败: %v", err)
			return
		}

		path := filepath.Join(dir, "bad_request.log")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Printf("打开错误日志文件失败: %v", err)
			return
		}

		errorFileLogger.file = f
		errorFileLogger.logger = log.New(f, "", 0) // 不加前缀，自行格式化
	})
	return errorFileLogger.logger
}

// logBadRequest 将 400 错误写入日志文件
func logBadRequest(endpoint string, model string, accountID int64, body []byte) {
	l := initErrorLogger()
	if l == nil {
		return
	}

	ts := time.Now().Format("2006/01/02 15:04:05")
	l.Printf("========== %s ==========\nEndpoint: %s\nModel: %s\nAccount: %d\nResponse:\n%s\n",
		ts, endpoint, model, accountID, string(body))
}

// CloseErrorLogger 关闭错误日志文件（程序退出时调用）
func CloseErrorLogger() {
	if errorFileLogger.file != nil {
		if err := errorFileLogger.file.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "关闭错误日志文件失败: %v\n", err)
		}
	}
}
