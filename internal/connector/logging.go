package connector

import (
	"encoding/json"
	"log"
	"strconv"

	"github.com/taufik/iso-connector-revarm/internal/iso8583"
)

var sensitiveFields = map[int]struct{}{
	2: {}, 35: {}, 36: {}, 45: {}, 48: {}, 52: {}, 53: {}, 55: {}, 102: {}, 103: {}, 104: {},
}

// LogFields returns ISO fields safe for operational logging. Authentication, card, account,
// PIN, track, private, and free-form transaction data are redacted.
func LogFields(message iso8583.Message) map[string]string {
	fields := make(map[string]string, len(message.Fields))
	for number, value := range message.Fields {
		key := strconv.Itoa(number)
		if _, sensitive := sensitiveFields[number]; sensitive {
			fields[key] = "***REDACTED***"
			continue
		}
		fields[key] = string(value)
	}
	return fields
}

func loggerOrDefault(logger *log.Logger) *log.Logger {
	if logger != nil {
		return logger
	}
	return log.Default()
}

func logISO(logger *log.Logger, component, direction, peer string, message iso8583.Message) {
	encoded, err := json.Marshal(LogFields(message))
	if err != nil {
		logger.Printf("event=iso component=%s direction=%s peer=%s fields=<encode-error>", component, direction, peer)
		return
	}
	logger.Printf("event=iso component=%s direction=%s peer=%s fields=%s", component, direction, peer, encoded)
}
