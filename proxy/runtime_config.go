package proxy

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/codex2api/database"
)

const (
	ClientCompatModePreserve = "preserve"
	ClientCompatModeAuto     = "auto"
	ClientCompatModeForce    = "force"

	StreamFlushPolicyImmediate = "immediate"
	StreamFlushPolicyCoalesce  = "coalesce"

	defaultClientCompatMode      = ClientCompatModePreserve
	defaultCodexMinCLIVersion    = "0.118.0"
	defaultStreamFlushPolicy     = StreamFlushPolicyImmediate
	defaultStreamFlushIntervalMS = 20
	minStreamFlushIntervalMS     = 1
	maxStreamFlushIntervalMS     = 1000
	defaultFirstTokenTimeoutSec  = 0
	maxFirstTokenTimeoutSec      = 600

	// 算法 1M：上下文窗口管理（摘要 + 结构化截断）默认值
	defaultContextWindowEnabled   = false
	defaultContextWindowThreshold = 272000
	defaultContextSummaryModel    = "gpt-5.4-mini"
	minContextWindowThreshold     = 1000
)

type RuntimeSettings struct {
	ClientCompatMode      string
	CodexMinCLIVersion    string
	StreamFlushPolicy     string
	StreamFlushIntervalMS int
	FirstTokenTimeoutSec  int

	// 算法 1M：先对最旧内容做一次摘要，摘要 + 较新内容若仍超阈值则结构化截断，
	// 强制 input ≤ 阈值；截断时对下游仍上报原始 input_tokens。
	ContextWindowEnabled   bool
	ContextWindowThreshold int
	ContextSummaryModel    string
}

var runtimeSettings atomic.Value // stores RuntimeSettings

func init() {
	runtimeSettings.Store(DefaultRuntimeSettings())
}

func DefaultRuntimeSettings() RuntimeSettings {
	return RuntimeSettings{
		ClientCompatMode:       defaultClientCompatMode,
		CodexMinCLIVersion:     defaultCodexMinCLIVersion,
		StreamFlushPolicy:      defaultStreamFlushPolicy,
		StreamFlushIntervalMS:  defaultStreamFlushIntervalMS,
		FirstTokenTimeoutSec:   defaultFirstTokenTimeoutSec,
		ContextWindowEnabled:   defaultContextWindowEnabled,
		ContextWindowThreshold: defaultContextWindowThreshold,
		ContextSummaryModel:    defaultContextSummaryModel,
	}
}

func NormalizeClientCompatMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ClientCompatModePreserve:
		return ClientCompatModePreserve
	case ClientCompatModeAuto:
		return ClientCompatModeAuto
	case ClientCompatModeForce:
		return ClientCompatModeForce
	default:
		return ClientCompatModePreserve
	}
}

func NormalizeStreamFlushPolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", StreamFlushPolicyImmediate:
		return StreamFlushPolicyImmediate
	case StreamFlushPolicyCoalesce:
		return StreamFlushPolicyCoalesce
	default:
		return StreamFlushPolicyImmediate
	}
}

func NormalizeRuntimeSettings(settings RuntimeSettings) RuntimeSettings {
	defaults := DefaultRuntimeSettings()
	settings.ClientCompatMode = NormalizeClientCompatMode(settings.ClientCompatMode)
	settings.StreamFlushPolicy = NormalizeStreamFlushPolicy(settings.StreamFlushPolicy)
	if strings.TrimSpace(settings.CodexMinCLIVersion) == "" {
		settings.CodexMinCLIVersion = defaults.CodexMinCLIVersion
	} else {
		settings.CodexMinCLIVersion = strings.TrimSpace(settings.CodexMinCLIVersion)
	}
	if settings.StreamFlushIntervalMS < minStreamFlushIntervalMS {
		settings.StreamFlushIntervalMS = defaults.StreamFlushIntervalMS
	}
	if settings.StreamFlushIntervalMS > maxStreamFlushIntervalMS {
		settings.StreamFlushIntervalMS = maxStreamFlushIntervalMS
	}
	if settings.FirstTokenTimeoutSec < 0 {
		settings.FirstTokenTimeoutSec = defaultFirstTokenTimeoutSec
	}
	if settings.FirstTokenTimeoutSec > maxFirstTokenTimeoutSec {
		settings.FirstTokenTimeoutSec = maxFirstTokenTimeoutSec
	}
	if settings.ContextWindowThreshold < minContextWindowThreshold {
		settings.ContextWindowThreshold = defaults.ContextWindowThreshold
	}
	if strings.TrimSpace(settings.ContextSummaryModel) == "" {
		settings.ContextSummaryModel = defaults.ContextSummaryModel
	} else {
		settings.ContextSummaryModel = strings.TrimSpace(settings.ContextSummaryModel)
	}
	return settings
}

func ApplyRuntimeSettingsFromSystem(settings *database.SystemSettings) RuntimeSettings {
	next := DefaultRuntimeSettings()
	if settings != nil {
		next.ClientCompatMode = settings.ClientCompatMode
		next.CodexMinCLIVersion = settings.CodexMinCLIVersion
		next.StreamFlushPolicy = settings.StreamFlushPolicy
		next.StreamFlushIntervalMS = settings.StreamFlushIntervalMS
		next.FirstTokenTimeoutSec = settings.FirstTokenTimeoutSeconds
		next.ContextWindowEnabled = settings.ContextWindowEnabled
		next.ContextWindowThreshold = settings.ContextWindowThreshold
		next.ContextSummaryModel = settings.ContextSummaryModel
	}
	next = NormalizeRuntimeSettings(next)
	runtimeSettings.Store(next)
	database.SetLongContextThreshold(next.ContextWindowThreshold)
	return next
}

func ApplyRuntimeSettings(settings RuntimeSettings) RuntimeSettings {
	settings = NormalizeRuntimeSettings(settings)
	runtimeSettings.Store(settings)
	database.SetLongContextThreshold(settings.ContextWindowThreshold)
	return settings
}

func CurrentRuntimeSettings() RuntimeSettings {
	if v, ok := runtimeSettings.Load().(RuntimeSettings); ok {
		return NormalizeRuntimeSettings(v)
	}
	return DefaultRuntimeSettings()
}

func currentStreamFlushInterval() time.Duration {
	ms := CurrentRuntimeSettings().StreamFlushIntervalMS
	if ms < minStreamFlushIntervalMS {
		ms = defaultStreamFlushIntervalMS
	}
	return time.Duration(ms) * time.Millisecond
}

func currentFirstTokenTimeout() time.Duration {
	seconds := CurrentRuntimeSettings().FirstTokenTimeoutSec
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
