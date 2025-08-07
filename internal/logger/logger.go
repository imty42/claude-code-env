package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// LogLevel 日志级别
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

var levelNames = map[LogLevel]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
}

var levelValues = map[string]LogLevel{
	"DEBUG": DEBUG,
	"INFO":  INFO,
	"WARN":  WARN,
	"ERROR": ERROR,
}

// Logger 统一日志系统
type Logger struct {
	level    LogLevel
	output   *log.Logger
	logFile  *os.File
}

var globalLogger *Logger

// InitLogger 初始化全局日志系统
func InitLogger(levelStr string) error {
	// 解析日志级别
	level, exists := levelValues[strings.ToUpper(levelStr)]
	if !exists {
		level = INFO // 默认 INFO 级别
	}

	// 创建日志文件
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("获取用户主目录失败: %v", err)
	}

	logPath := filepath.Join(homeDir, ".claude-code-env", "ccenv.log")
	
	// 确保目录存在
	err = os.MkdirAll(filepath.Dir(logPath), 0755)
	if err != nil {
		return fmt.Errorf("创建日志目录失败: %v", err)
	}

	// 打开日志文件
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("创建日志文件失败: %v", err)
	}

	// 创建日志器
	output := log.New(logFile, "", log.LstdFlags)

	globalLogger = &Logger{
		level:   level,
		output:  output,
		logFile: logFile,
	}

	return nil
}

// CloseLogger 关闭日志系统
func CloseLogger() {
	if globalLogger != nil && globalLogger.logFile != nil {
		globalLogger.logFile.Close()
	}
}

// shouldLog 检查是否应该记录此级别的日志
func shouldLog(msgLevel LogLevel) bool {
	if globalLogger == nil {
		return true // 如果未初始化，记录所有日志
	}
	return msgLevel >= globalLogger.level
}

// log 统一日志记录方法
func logMessage(level LogLevel, module, message string, args ...interface{}) {
	if !shouldLog(level) {
		return
	}

	levelName := levelNames[level]
	formattedMsg := fmt.Sprintf(message, args...)
	logLine := fmt.Sprintf("[%s] %s %s", levelName, module, formattedMsg)

	if globalLogger != nil {
		globalLogger.output.Println(logLine)
	} else {
		// 如果日志系统未初始化，输出到标准错误
		log.Println(logLine)
	}
}

// Debug 记录 DEBUG 级别日志
func Debug(module, message string, args ...interface{}) {
	logMessage(DEBUG, module, message, args...)
}

// Info 记录 INFO 级别日志
func Info(module, message string, args ...interface{}) {
	logMessage(INFO, module, message, args...)
}

// Warn 记录 WARN 级别日志
func Warn(module, message string, args ...interface{}) {
	logMessage(WARN, module, message, args...)
}

// Error 记录 ERROR 级别日志
func Error(module, message string, args ...interface{}) {
	logMessage(ERROR, module, message, args...)
}

// 预定义的模块名称常量
const (
	ModuleProxy    = "PROXY"
	ModuleConfig   = "CONFIG"
	ModuleExecutor = "EXECUTOR"
	ModuleServer   = "SERVER"
	ModuleProvider = "PROVIDER"
)