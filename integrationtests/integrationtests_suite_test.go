package integrationtests

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"

	"strconv"

	"github.com/lucas-clemente/quic-go/h2quic"
	"github.com/lucas-clemente/quic-go/testdata"
	"github.com/lucas-clemente/quic-go/utils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

const (
	dataLen     = 500 * 1024       // 500 KB
	dataLongLen = 50 * 1024 * 1024 // 50 MB
)

var (
	server     *h2quic.Server
	dataMan    dataManager
	port       string
	clientPath string // path of the quic_client
	serverPath string // path of the quic_server

	logFileName string // the log file set in the ginkgo flags
	logFile     *os.File

	nFilesUploaded int32
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Integration Tests Suite")
}

var _ = BeforeSuite(func() {
	setupHTTPHandlers()
	setupQuicServer()
})

var _ = AfterSuite(func() {
	err := server.Close()
	Expect(err).NotTo(HaveOccurred())
}, 10)

// read the logfile command line flag
// to set call ginkgo -- -logfile=log.txt
func init() {
	flag.StringVar(&logFileName, "logfile", "", "log file")
}

var _ = BeforeEach(func() {
	_, thisfile, _, ok := runtime.Caller(0)
	if !ok {
		Fail("Failed to get current path")
	}
	clientPath = filepath.Join(thisfile, fmt.Sprintf("../../../quic-clients/client-%s-debug", runtime.GOOS))
	serverPath = filepath.Join(thisfile, fmt.Sprintf("../../../quic-clients/server-%s-debug", runtime.GOOS))

	nFilesUploaded = 0

	if len(logFileName) > 0 {
		var err error
		logFile, err = os.Create("./log.txt")
		Expect(err).ToNot(HaveOccurred())
		utils.SetLogWriter(logFile)
		utils.SetLogLevel(utils.LogLevelDebug)
	}
})

var _ = AfterEach(func() {
	if len(logFileName) > 0 {
		_ = logFile.Close()
	}
})

func setupHTTPHandlers() {
	defer GinkgoRecover()

	http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		_, err := io.WriteString(w, "Hello, World!\n")
		Expect(err).NotTo(HaveOccurred())
	})

	http.HandleFunc("/data", func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		data := dataMan.GetData()
		Expect(data).ToNot(HaveLen(0))
		_, err := w.Write(data)
		Expect(err).NotTo(HaveOccurred())
	})

	http.HandleFunc("/prdata", func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		sl := r.URL.Query().Get("len")
		l, err := strconv.Atoi(sl)
		Expect(err).NotTo(HaveOccurred())
		data := generatePRData(l)
		_, err = w.Write(data)
		Expect(err).NotTo(HaveOccurred())
	})

	http.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		body, err := ioutil.ReadAll(r.Body)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write(body)
		Expect(err).NotTo(HaveOccurred())
	})

	// Requires the len & num GET parameters, e.g. /uploadtest?len=100&num=1
	http.HandleFunc("/uploadtest", func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		response := uploadHTML
		response = strings.Replace(response, "LENGTH", r.URL.Query().Get("len"), -1)
		response = strings.Replace(response, "NUM", r.URL.Query().Get("num"), -1)
		_, err := io.WriteString(w, response)
		Expect(err).NotTo(HaveOccurred())
	})

	// Requires the len & num GET parameters, e.g. /downloadtest?len=100&num=1
	http.HandleFunc("/downloadtest", func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		response := downloadHTML
		response = strings.Replace(response, "LENGTH", r.URL.Query().Get("len"), -1)
		response = strings.Replace(response, "NUM", r.URL.Query().Get("num"), -1)
		_, err := io.WriteString(w, response)
		Expect(err).NotTo(HaveOccurred())
	})

	http.HandleFunc("/uploadhandler", func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()

		l, err := strconv.Atoi(r.URL.Query().Get("len"))
		Expect(err).NotTo(HaveOccurred())

		defer r.Body.Close()
		actual, err := ioutil.ReadAll(r.Body)
		Expect(err).NotTo(HaveOccurred())

		Expect(bytes.Equal(actual, generatePRData(l))).To(BeTrue())

		atomic.AddInt32(&nFilesUploaded, 1)
	})
}

func setupQuicServer() {
	server = &h2quic.Server{
		Server: &http.Server{
			TLSConfig: testdata.GetTLSConfig(),
		},
	}

	addr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
	Expect(err).NotTo(HaveOccurred())
	conn, err := net.ListenUDP("udp", addr)
	Expect(err).NotTo(HaveOccurred())
	port = strconv.Itoa(conn.LocalAddr().(*net.UDPAddr).Port)

	go func() {
		defer GinkgoRecover()
		server.Serve(conn)
	}()
}

const prngJS = `
var buf = new ArrayBuffer(LENGTH);
var prng = new Uint8Array(buf);
var seed = 1;
for (var i = 0; i < LENGTH; i++) {
	// https://en.wikipedia.org/wiki/Lehmer_random_number_generator
	seed = seed * 48271 % 2147483647;
	prng[i] = seed;
}
`

const uploadHTML = `
<html>
<body>
<script>
  ` + prngJS + `
	for (var i = 0; i < NUM; i++) {
		var req = new XMLHttpRequest();
		req.open("POST", "/uploadhandler?len=" + LENGTH, true);
		req.send(buf);
	}
</script>
</body>
</html>
`

const downloadHTML = `
<html>
<body>
<script>
	` + prngJS + `

	function verify(data) {
		if (data.length !== LENGTH) return false;
		for (var i = 0; i < LENGTH; i++) {
			if (data[i] !== prng[i]) return false;
		}
		return true;
	}

	var nOK = 0;
	for (var i = 0; i < NUM; i++) {
		let req = new XMLHttpRequest();
		req.responseType = "arraybuffer";
		req.open("POST", "/prdata?len=" + LENGTH, true);
		req.onreadystatechange = function () {
			if (req.readyState === XMLHttpRequest.DONE && req.status === 200) {
				if (verify(new Uint8Array(req.response))) {
					nOK++;
					if (nOK === NUM) {
						document.write("dltest ok");
					}
				}
			}
		};
		req.send();
	}
</script>
</body>
</html>
`

func generatePRData(l int) []byte {
	res := make([]byte, l)
	seed := uint64(1)
	for i := 0; i < l; i++ {
		seed = seed * 48271 % 2147483647
		res[i] = byte(seed)
	}
	return res
}
