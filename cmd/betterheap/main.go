// Command betterheap is an agent-friendly CLI for Better Stack logs that
// queries the live hot buffer and the S3 archive as one continuous window.
package main

import "github.com/jpaddison3/betterheap/internal/cli"

func main() {
	cli.Execute()
}
