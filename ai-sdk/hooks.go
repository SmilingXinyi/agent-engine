package ai

import "github.com/SmilingXinyi/agent-engine/ai-sdk/protocol"

func cloneRequestInfo(info protocol.RequestInfo) protocol.RequestInfo {
	info.Request = info.Request.Clone()
	return info
}

func cloneResponseInfo(info protocol.ResponseInfo) protocol.ResponseInfo {
	info.Request = info.Request.Clone()
	info.Response = info.Response.Clone()
	return info
}
