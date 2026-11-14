package main

import "context"

// The program docs/examples.md shows runs and prints what it did.
func Example() {
	if err := run(context.Background()); err != nil {
		panic(err)
	}
	// Output:
	// created context handoff
	// appended message 1
	// read message 1: Please review the patch.
	// claimed task "Review the patch": leased, lease version 1
}
