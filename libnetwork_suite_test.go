package main

import (
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

func TestLibnetwork(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Libnetwork Suite")
}
