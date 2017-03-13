package integrationtests

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/knq/chromedp"

	"github.com/knq/chromedp/runner"
	"github.com/lucas-clemente/quic-go/protocol"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const nImgs = 200
const imgSize = 40
const chromeDownloadWait = 200 * time.Millisecond

func init() {
	http.HandleFunc("/tile", func(w http.ResponseWriter, r *http.Request) {
		// Small 40x40 png
		w.Write([]byte{
			0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
			0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x28, 0x00, 0x00, 0x00, 0x28,
			0x01, 0x03, 0x00, 0x00, 0x00, 0xb6, 0x30, 0x2a, 0x2e, 0x00, 0x00, 0x00,
			0x03, 0x50, 0x4c, 0x54, 0x45, 0x5a, 0xc3, 0x5a, 0xad, 0x38, 0xaa, 0xdb,
			0x00, 0x00, 0x00, 0x0b, 0x49, 0x44, 0x41, 0x54, 0x78, 0x01, 0x63, 0x18,
			0x61, 0x00, 0x00, 0x00, 0xf0, 0x00, 0x01, 0xe2, 0xb8, 0x75, 0x22, 0x00,
			0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
		})
	})

	http.HandleFunc("/tiles", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<html><body>")
		for i := 0; i < nImgs; i++ {
			fmt.Fprintf(w, `<img src="/tile?cachebust=%d">`, i)
		}
		io.WriteString(w, "</body></html>")
	})
}

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

			It("loads a simple hello world page using quic", func(done Done) {
				navigate(ctx, chrome, "https://quic.clemente.io/hello")
				Eventually(func() string {
					res := ""
					err := chrome.Run(ctx, chromedp.Tasks{chromedp.Text("body", &res)})
					Expect(err).NotTo(HaveOccurred())
					return res
				}).Should(ContainSubstring("Hello, World!"))
				close(done)
			}, 5)

			It("loads a large number of files", func(done Done) {
				expectedWidth := nImgs * imgSize
				navigate(ctx, chrome, "https://quic.clemente.io/tiles")
				Eventually(func() error {
					totalWidth := []byte{}
					chrome.Run(ctx, chromedp.Tasks{
						chromedp.Evaluate(`var w = 0; document.querySelectorAll("img").forEach(e=> w += e.offsetWidth); w`, &totalWidth),
					})
					if string(totalWidth) != strconv.Itoa(expectedWidth) {
						return fmt.Errorf("expected %d, got %s", expectedWidth, string(totalWidth))
					}
					return nil
				}, 5).ShouldNot(HaveOccurred())
				close(done)
			}, 10)

			It("downloads a small file", func() {
				dlName := getRandomDlName()
				dataMan.GenerateData(dataLen)
				navigate(ctx, chrome, "https://quic.clemente.io/data/"+dlName)
				Eventually(func() []byte { return getDownloadMD5(dlName) }, 10, 0.1).Should(Equal(dataMan.GetMD5()))
				// To avoid Chrome's "do you want to abort the DL" window
				time.Sleep(chromeDownloadWait)
			}, 10)

			It("downloads a large file", func() {
				dlName := getRandomDlName()
				dataMan.GenerateData(dataLongLen)
				err := chrome.Run(ctx, chromedp.Tasks{
					chromedp.Navigate("https://quic.clemente.io/data/" + dlName),
				})
				Expect(err).NotTo(HaveOccurred())
				Eventually(func() int { return getDownloadSize(dlName) }, 10, 0.5).Should(Equal(dataLongLen))
				Expect(getDownloadMD5(dlName)).To(Equal(dataMan.GetMD5()))
				// To avoid Chrome's "do you want to abort the DL" window
				time.Sleep(chromeDownloadWait)
			}, 10)

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
