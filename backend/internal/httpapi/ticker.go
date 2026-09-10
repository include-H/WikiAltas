package httpapi

import "time"

func heartbeatTicker() *time.Ticker {
	return time.NewTicker(15 * time.Second)
}
