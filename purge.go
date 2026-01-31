package main

import "time"

func purgeExpiredLoop() {
	if purgeInterval <= 0 {
		return
	}
	ticker := time.NewTicker(purgeInterval)
	defer ticker.Stop()
	for range ticker.C {
		purgeExpiredOnce()
	}
}
