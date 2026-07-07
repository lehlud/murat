package protocol

import "lehnert.dev/murat/internal/textutil"

func cleanHeaderValue(value string) string {
	return textutil.CleanHeaderValue(value)
}
