package version

import (
	"runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestVersion(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Version Suite")
}

var _ = Describe("GetFullVersion", func() {
	var (
		origVersion       string
		origSHA           string
		origTreeState     string
		origReleaseStatus string
	)

	BeforeEach(func() {
		origVersion = Version
		origSHA = GitSHA
		origTreeState = GitTreeState
		origReleaseStatus = ReleaseStatus
	})

	AfterEach(func() {
		Version = origVersion
		GitSHA = origSHA
		GitTreeState = origTreeState
		ReleaseStatus = origReleaseStatus
	})

	It("returns UNKNOWN when Version is empty", func() {
		Version = ""
		Expect(GetFullVersion()).To(Equal("UNKNOWN"))
	})

	It("returns just Version when released", func() {
		Version = "v1.0.0"
		ReleaseStatus = "released"
		Expect(GetFullVersion()).To(Equal("v1.0.0"))
	})

	It("returns version-SHA when unreleased with clean tree", func() {
		Version = "v1.0.0"
		ReleaseStatus = "unreleased"
		GitSHA = "abc123"
		GitTreeState = "clean"
		Expect(GetFullVersion()).To(Equal("v1.0.0-abc123"))
	})

	It("returns version-SHA.dirty when unreleased with dirty tree", func() {
		Version = "v1.0.0"
		ReleaseStatus = "unreleased"
		GitSHA = "abc123"
		GitTreeState = "dirty"
		Expect(GetFullVersion()).To(Equal("v1.0.0-abc123.dirty"))
	})

	It("returns version-unknown when unreleased with no SHA", func() {
		Version = "v1.0.0"
		ReleaseStatus = "unreleased"
		GitSHA = ""
		Expect(GetFullVersion()).To(Equal("v1.0.0-unknown"))
	})
})

var _ = Describe("GetFullVersionWithRuntimeInfo", func() {
	It("includes GOOS and GOARCH", func() {
		origVersion := Version
		defer func() { Version = origVersion }()
		Version = ""
		result := GetFullVersionWithRuntimeInfo()
		Expect(result).To(HavePrefix("UNKNOWN"))
		Expect(result).To(ContainSubstring(runtime.GOOS))
		Expect(result).To(ContainSubstring(runtime.GOARCH))
	})

	It("formats as 'version GOOS/GOARCH'", func() {
		origVersion := Version
		origReleaseStatus := ReleaseStatus
		defer func() {
			Version = origVersion
			ReleaseStatus = origReleaseStatus
		}()
		Version = "v2.0.0"
		ReleaseStatus = "released"
		expected := "v2.0.0 " + runtime.GOOS + "/" + runtime.GOARCH
		Expect(GetFullVersionWithRuntimeInfo()).To(Equal(expected))
	})
})
