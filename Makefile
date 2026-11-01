# Build the server for the Linux host and put it into service there.
#
#   make deploy
#
# The binary goes to the host's /tmp first, because only the juke user can
# write to its bin directory.  Installing and restarting happen under one
# sudo, so there's one password prompt.  -- claude, 2026-11-01

HOST   = boojum
BINARY = var/juke-linux-amd64
TARGET = /home/juke/bin/juke

.PHONY: deploy linux clean

linux: $(BINARY)

$(BINARY): FORCE
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $@ ./cmd/juke

deploy: $(BINARY)
	scp $(BINARY) $(HOST):/tmp/juke.new
	ssh -t $(HOST) 'sudo sh -c "install -o juke -g juke -m 755 /tmp/juke.new $(TARGET) && rm /tmp/juke.new && systemctl restart juke && systemctl --no-pager status juke | head -n 3"'

clean:
	rm -f $(BINARY)

FORCE:
