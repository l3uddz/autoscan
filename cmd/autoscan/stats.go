package main

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/l3uddz/autoscan/processor"
)

func scanStats(ctx context.Context, proc *processor.Processor, interval time.Duration) {
	st := time.NewTicker(interval)
	defer st.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-st.C:
			// retrieve amount of scans remaining
			sm, err := proc.ScansRemaining()
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Error().
					Err(err).
					Msg("Failed determining amount of remaining scans")
				continue
			}

			log.Info().
				Int("remaining", sm).
				Int64("processed", proc.ScansProcessed()).
				Int64("skipped", proc.ScansSkipped()).
				Msg("Scan stats")
		}
	}
}
