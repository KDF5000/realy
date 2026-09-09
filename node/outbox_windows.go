package node

import (
	"errors"
	"os"
)

func lockOutbox(string) (*os.File, error) {
	return nil, errors.New("durable outbox is currently supported on Linux and macOS")
}
