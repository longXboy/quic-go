package integrationtests

import (
	"context"
	"fmt"
	"strconv"

	"github.com/knq/chromedp"

	"github.com/knq/chromedp/runner"
	"github.com/lucas-clemente/quic-go/protocol"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func getChromeForVersion(ctx context.Context, version protocol.VersionNumber) *chromedp.CDP {
	cdp, err := chromedp.New(ctx, chromedp.WithRunnerOptions(
		runner.Flag("enable-quic", true),
		runner.Flag("no-proxy-server", true),
		runner.Flag("origin-to-force-quic-on", "quic.clemente.io:443"),
		runner.Flag("host-resolver-rules",
			fmt.Sprintf("MAP quic.clemente.io:443 localhost:%s", port)),
		runner.Flag("quic-version", fmt.Sprintf("QUIC_VERSION_%d", version)),
	))
	Expect(err).NotTo(HaveOccurred())
	return cdp
}

func navigate(ctx context.Context, chrome *chromedp.CDP, url string) {
	err := chrome.Run(ctx, chromedp.Tasks{
		chromedp.Navigate(url),
	})
	Expect(err).NotTo(HaveOccurred())
}

func waitForText(ctx context.Context, chrome *chromedp.CDP, text string) {
	Eventually(func() string {
		var res string
		err := chrome.Run(ctx, chromedp.Tasks{chromedp.Text("body", &res)})
		Expect(err).NotTo(HaveOccurred())
		return res
	}, 30).Should(ContainSubstring(text))
}

var _ = Describe("Chrome tests", func() {
	// test disabled since it doesn't work with the configurable QUIC version in the server
	PIt("does not work with mismatching versions", func() {
		versionForUs := protocol.SupportedVersions[0]
		versionForChrome := protocol.SupportedVersions[1]

		supportedVersionsBefore := protocol.SupportedVersions
		protocol.SupportedVersions = []protocol.VersionNumber{versionForUs}

		ctx, cancelChrome := context.WithCancel(context.Background())
		chrome := getChromeForVersion(ctx, versionForChrome)

		defer func() {
			protocol.SupportedVersions = supportedVersionsBefore
			err := chrome.Shutdown(ctx)
			Expect(err).NotTo(HaveOccurred())
			err = chrome.Wait()
			Expect(err).NotTo(HaveOccurred())
			cancelChrome()
		}()

		navigate(ctx, chrome, "https://quic.clemente.io/hello")
		Consistently(func() string {
			res := ""
			chrome.Run(ctx, chromedp.Tasks{chromedp.Text("body", &res)})
			return res
		}).ShouldNot(ContainSubstring("Hello, World!"))
	})

	for i := range protocol.SupportedVersions {
		version := protocol.SupportedVersions[i]

		Context(fmt.Sprintf("with quic version %d", version), func() {
			var (
				ctx                     context.Context
				cancelChrome            context.CancelFunc
				chrome                  *chromedp.CDP
				supportedVersionsBefore []protocol.VersionNumber
			)

			BeforeEach(func() {
				if version == protocol.Version37 {
					Skip("Skipping Chrome test with QUIC version 37")
				}
				supportedVersionsBefore = protocol.SupportedVersions
				protocol.SupportedVersions = []protocol.VersionNumber{version}
				ctx, cancelChrome = context.WithCancel(context.Background())
				chrome = getChromeForVersion(ctx, version)
			})

			AfterEach(func() {
				defer cancelChrome()
				err := chrome.Shutdown(ctx)
				Expect(err).NotTo(HaveOccurred())
				err = chrome.Wait()
				Expect(err).NotTo(HaveOccurred())
				protocol.SupportedVersions = supportedVersionsBefore
			})

			It("loads a simple hello world page using quic", func() {
				navigate(ctx, chrome, "https://quic.clemente.io/hello")
				waitForText(ctx, chrome, "Hello, World!")
			})

			It("downloads a small file", func() {
				navigate(ctx, chrome, "https://quic.clemente.io/downloadtest?num=1&len="+strconv.Itoa(dataLen))
				waitForText(ctx, chrome, "dltest ok")
			})

			It("downloads a large file", func() {
				navigate(ctx, chrome, "https://quic.clemente.io/downloadtest?num=1&len="+strconv.Itoa(dataLongLen))
				waitForText(ctx, chrome, "dltest ok")
			})

			It("loads a large number of files", func() {
				navigate(ctx, chrome, "https://quic.clemente.io/downloadtest?num=4&len=100")
				waitForText(ctx, chrome, "dltest ok")
			})

			It("uploads a small file", func() {
				navigate(ctx, chrome, "https://quic.clemente.io/uploadtest?num=1&len="+strconv.Itoa(dataLen))
				Eventually(func() int32 { return nFilesUploaded }).Should(BeEquivalentTo(1))
			})

			It("uploads a large file", func() {
				navigate(ctx, chrome, "https://quic.clemente.io/uploadtest?num=1&len="+strconv.Itoa(dataLongLen))
				Eventually(func() int32 { return nFilesUploaded }, 30).Should(BeEquivalentTo(1))
			})

			It("uploads many small files", func() {
				num := protocol.MaxStreamsPerConnection + 20
				navigate(ctx, chrome, "https://quic.clemente.io/uploadtest?len="+strconv.Itoa(dataLen)+"&num="+strconv.Itoa(num))
				Eventually(func() int32 { return nFilesUploaded }, 30).Should(BeEquivalentTo(num))
			})
		})
	}
})
