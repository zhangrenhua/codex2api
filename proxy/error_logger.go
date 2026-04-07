package proxy

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/codex2api/security"
)

// fileLogger 单个日志文件实例（带大小轮转）
type fileLogger struct {
	mu     sync.Mutex
	logger *log.Logger
	file   *os.File
	path   string
	size   int64 // 当前文件大小（字节）
}

var (
	badRequestLogger  = &fileLogger{path: "bad_request.log"}  // 400 错误
	serverErrorLogger = &fileLogger{path: "server_error.log"} // 5xx 错误
)

const (
	logDir         = "logs"
	logMaxSize     = 50 * 1024 * 1024 // 单文件最大 50MB
	logBackupCount = 2                // 保留备份数
)

// init 初始化或重新打开日志文件
func (fl *fileLogger) openFile() error {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}
	fullPath := filepath.Join(logDir, fl.path)
	f, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("打开日志文件 %s 失败: %w", fl.path, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	fl.file = f
	fl.size = info.Size()
	fl.logger = log.New(f, "", 0)
	return nil
}

// rotate 轮转日志文件：关闭当前文件 → 重命名 → 打开新文件
func (fl *fileLogger) rotate() {
	if fl.file != nil {
		fl.file.Close()
		fl.file = nil
		fl.logger = nil
	}

	fullPath := filepath.Join(logDir, fl.path)

	// 删除最老的备份，依次重命名: .2 → 删除, .1 → .2, 当前 → .1
	for i := logBackupCount; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", fullPath, i)
		if i == logBackupCount {
			os.Remove(src)
		} else {
			dst := fmt.Sprintf("%s.%d", fullPath, i+1)
			os.Rename(src, dst)
		}
	}
	os.Rename(fullPath, fullPath+".1")

	if err := fl.openFile(); err != nil {
		log.Printf("日志轮转后重新打开失败: %v", err)
	}
}

func (fl *fileLogger) close() {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	if fl.file != nil {
		if err := fl.file.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "关闭日志文件 %s 失败: %v\n", fl.path, err)
		}
	}
}

// writeEntry 写一条错误日志（自动脱敏敏感信息，超限自动轮转）
func (fl *fileLogger) writeEntry(endpoint string, statusCode int, model string, accountID int64, body []byte) {
	fl.mu.Lock()
	defer fl.mu.Unlock()

	// 延迟初始化
	if fl.file == nil {
		if err := fl.openFile(); err != nil {
			log.Printf("%v", err)
			return
		}
	}

	// 脱敏日志内容
	safeEndpoint := security.SanitizeLog(endpoint)
	safeModel := security.SanitizeLog(model)
	bodyStr := string(body)

	// 检查并脱敏响应体中的敏感信息
	bodyStr = security.MaskSensitiveData(bodyStr)
	bodyStr = security.SafeTruncate(bodyStr, 5000) // 限制日志大小

	ts := time.Now().Format("2006/01/02 15:04:05")
	entry := fmt.Sprintf("========== %s ==========\nEndpoint: %s\nStatus: %d\nModel: %s\nAccount: %d\nResponse:\n%s\n",
		ts, safeEndpoint, statusCode, safeModel, accountID, bodyStr)

	fl.logger.Print(entry)
	fl.size += int64(len(entry))

	// 超过大小限制时轮转
	if fl.size >= logMaxSize {
		fl.rotate()
	}
}

// logUpstreamError 根据状态码分发到对应日志文件
func logUpstreamError(endpoint string, statusCode int, model string, accountID int64, body []byte) {
	switch {
	case statusCode == 400:
		badRequestLogger.writeEntry(endpoint, statusCode, model, accountID, body)
	case statusCode >= 500:
		serverErrorLogger.writeEntry(endpoint, statusCode, model, accountID, body)
	}
}

// CloseErrorLogger 关闭所有错误日志文件（程序退出时调用）
func CloseErrorLogger() {
	badRequestLogger.close()
	serverErrorLogger.close()
}
