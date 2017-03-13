package integrationtests

import (
	"bytes"
	"crypto/md5"
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
	"time"

	"strconv"

	"github.com/lucas-clemente/quic-go/h2quic"
	"github.com/lucas-clemente/quic-go/testdata"
	"github.com/lucas-clemente/quic-go/utils"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"testing"
)

const (
	dataLen      = 500 * 1024       // 500 KB
	dataLongLen  = 50 * 1024 * 1024 // 50 MB
	dlDataPrefix = "quic-go_dl_test_"
)

var (
	server         *h2quic.Server
	dataMan        dataManager
	port           string
	downloadDir    string
	clientPath     string // path of the quic_client
	serverPath     string // path of the quic_server

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

	downloadDir = os.Getenv("HOME") + "/Downloads/"
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
	removeDownloadData()
	if len(uploadDir) < 20 {
		panic("uploadDir too short")
	}
	os.RemoveAll(uploadDir)

	// remove downloaded file in docker container
	removeDownload("data")

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

	http.HandleFunc("/data/", func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		data := dataMan.GetData()
		Expect(data).ToNot(HaveLen(0))
		_, err := w.Write(data)
		Expect(err).NotTo(HaveOccurred())
	})

	http.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		body, err := ioutil.ReadAll(r.Body)
		Expect(err).NotTo(HaveOccurred())
		_, err = w.Write(body)
		Expect(err).NotTo(HaveOccurred())
	})

	// Requires the len & num GET parameters, e.g. /uploadform?len=100&num=1
	http.HandleFunc("/uploadtest", func(w http.ResponseWriter, r *http.Request) {
		defer GinkgoRecover()
		response := uploadHTML
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

// getDownloadSize gets the file size of a file in the local download folder
func getDownloadSize(filename string) int {
	stat, err := os.Stat(downloadDir + filename)
	if err != nil {
		return 0
	}
	return int(stat.Size())
}

// getDownloadMD5 gets the md5 sum file of a file in the local download folder
func getDownloadMD5(filename string) []byte {
	return getFileMD5(filepath.Join(downloadDir, filename))
}

func getFileMD5(filename string) []byte {
	var result []byte
	file, err := os.Open(filename)
	if err != nil {
		return nil
	}
	defer file.Close()

	hash := md5.New()
	_, err = io.Copy(hash, file)
	if err != nil {
		return nil
	}
	return hash.Sum(result)
}

func getRandomDlName() string {
	return dlDataPrefix + strconv.Itoa(time.Now().Nanosecond())
}

func removeDownloadData() {
	pattern := downloadDir + dlDataPrefix + "*"
	if len(pattern) < 10 || !strings.Contains(pattern, "quic-go") {
		panic("DL dir looks weird: " + pattern)
	}
	paths, err := filepath.Glob(pattern)
	Expect(err).NotTo(HaveOccurred())
	if len(paths) > 2 {
		panic("warning: would have deleted too many files, pattern " + pattern)
	}
	for _, path := range paths {
		err = os.Remove(path)
		Expect(err).NotTo(HaveOccurred())
	}
}

const uploadHTML = `
<html>
<body>
<script>
  var buf = new ArrayBuffer(LENGTH);
  var arr = new Uint8Array(buf);
  var seed = 1;
  for (var i = 0; i < LENGTH; i++) {
    // https://en.wikipedia.org/wiki/Lehmer_random_number_generator
    seed = seed * 48271 % 2147483647;
    arr[i] = seed;
  }
	for (var i = 0; i < NUM; i++) {
		var req = new XMLHttpRequest();
		req.open("POST", "/uploadhandler?len=" + LENGTH, true);
		req.send(buf);
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
