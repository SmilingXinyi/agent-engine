package agent

// EngineOption configures the default agent engine behavior.
type EngineOption func(*engineConfig)

type engineConfig struct {
	maxMessages int
	debugSink   DebugSink
	factory     ModelClientFactory
}

func newEngineConfig(options ...EngineOption) engineConfig {
	var config engineConfig
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return config
}

// WithMaxMessages limits the number of history messages passed to the model provider.
// Zero (the default) means no limit — all history messages are forwarded.
func WithMaxMessages(maxMessages int) EngineOption {
	return func(config *engineConfig) {
		config.maxMessages = maxMessages
	}
}

// WithDebugSink attaches a DebugSink that receives every outbound request payload.
// Pass nil to disable dumping (the default).
func WithDebugSink(sink DebugSink) EngineOption {
	return func(config *engineConfig) {
		config.debugSink = sink
	}
}

// WithProvider sets the ModelClientFactory used to create model clients.
func WithProvider(factory ModelClientFactory) EngineOption {
	return func(config *engineConfig) {
		config.factory = factory
	}
}
