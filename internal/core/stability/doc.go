// Package stability holds the vocabulary for calling something that can fail:
// what an error is worth, how many attempts it is worth, and when to stop
// making them. It is domain-free on purpose — a storage mount and a model
// endpoint are both far ends, and one answer to "is this worth another
// attempt" is what keeps them from drifting into two.
package stability
