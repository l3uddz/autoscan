package main

import (
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autoscan/processor"
)

func scanStats(proc *processor.Processor, interval time.Duration) {
	st := time.NewTicker(interval)
	defer st.Stop()

	for range st.C {
		// retrieve amount of scans remaining
		sm, err := proc.ScansRemaining()
		if err != nil {
			log.Error().
				Err(err).
				Msg("Failed determining amount of remaining scans")
			continue
		}

		log.Info().
			Int("remaining", sm).
			Int64("processed", proc.ScansProcessed()).
			Msg("Scan stats")
	}
}
