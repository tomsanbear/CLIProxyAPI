package config

const (
	DefaultPanelGitHubRepository = "https://github.com/router-for-me/Cli-Proxy-API-Management-Center"
	DefaultPprofAddr             = "127.0.0.1:8316"
	DefaultAuthDir               = "~/.cli-proxy-api"
	DefaultDiscoveryServiceType  = "_ai-gateway._tcp"

	DefaultArchiveSegmentMaxBytes int64 = 64 << 20
	DefaultArchiveSegmentMaxAge         = "15m"
	DefaultArchiveRingSegments          = 16
	DefaultArchiveMaxRecordBytes  int64 = 64 << 20
)
