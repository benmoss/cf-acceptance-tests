package apps

import (
	"regexp"
	"time"

	. "github.com/cloudfoundry/cf-acceptance-tests/cats_suite_helpers"

	"github.com/cloudfoundry-incubator/cf-test-helpers/cf"
	"github.com/cloudfoundry-incubator/cf-test-helpers/helpers"
	"github.com/cloudfoundry/cf-acceptance-tests/helpers/app_helpers"
	"github.com/cloudfoundry/cf-acceptance-tests/helpers/assets"
	"github.com/cloudfoundry/cf-acceptance-tests/helpers/random_name"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gbytes"
	. "github.com/onsi/gomega/gexec"
)

var _ = AppsDescribe("Logging", func() {
	var logWriterAppName string
	var listenerAppName string
	var logs *Session
	var interrupt chan struct{}
	var serviceName string

	Describe("Syslog drains", func() {
		BeforeEach(func() {
			interrupt = make(chan struct{}, 1)
			serviceName = random_name.CATSRandomName("SVCINS")
			listenerAppName = random_name.CATSRandomName("APP")
			logWriterAppName = random_name.CATSRandomName("APP")

			Eventually(cf.Cf("push", listenerAppName, "--no-start", "--health-check-type", "port", "-b", config.GoBuildpackName, "-m", DEFAULT_MEMORY_LIMIT, "-p", assets.NewAssets().SyslogDrainListener, "-d", Config.AppsDomain, "-f", assets.NewAssets().SyslogDrainListener+"/manifest.yml"), DEFAULT_TIMEOUT).Should(Exit(0), "Failed to push app")
			Eventually(cf.Cf("push", logWriterAppName, "--no-start", "-b", Config.RubyBuildpackName, "-m", DEFAULT_MEMORY_LIMIT, "-p", assets.NewAssets().RubySimple, "-d", Config.AppsDomain), DEFAULT_TIMEOUT).Should(Exit(0), "Failed to push app")

			app_helpers.SetBackend(listenerAppName)
			app_helpers.SetBackend(logWriterAppName)

			Expect(cf.Cf("start", listenerAppName).Wait(CF_PUSH_TIMEOUT)).To(Exit(0))
			Expect(cf.Cf("start", logWriterAppName).Wait(CF_PUSH_TIMEOUT)).To(Exit(0))
		})

		AfterEach(func() {
			logs.Kill()
			close(interrupt)

			app_helpers.AppReport(logWriterAppName, DEFAULT_TIMEOUT)
			app_helpers.AppReport(listenerAppName, DEFAULT_TIMEOUT)

			Eventually(cf.Cf("delete", logWriterAppName, "-f", "-r"), DEFAULT_TIMEOUT).Should(Exit(0), "Failed to delete app")
			Eventually(cf.Cf("delete", listenerAppName, "-f", "-r"), DEFAULT_TIMEOUT).Should(Exit(0), "Failed to delete app")
			if serviceName != "" {
				Eventually(cf.Cf("delete-service", serviceName, "-f"), DEFAULT_TIMEOUT).Should(Exit(0), "Failed to delete service")
			}

			Eventually(cf.Cf("delete-orphaned-routes", "-f"), CF_PUSH_TIMEOUT).Should(Exit(0), "Failed to delete orphaned routes")
		})

		It("forwards app messages to registered syslog drains", func() {
			syslogDrainURL := "syslog://" + getSyslogDrainAddress(listenerAppName)

			Eventually(cf.Cf("cups", serviceName, "-l", syslogDrainURL), DEFAULT_TIMEOUT).Should(Exit(0), "Failed to create syslog drain service")
			Eventually(cf.Cf("bind-service", logWriterAppName, serviceName), DEFAULT_TIMEOUT).Should(Exit(0), "Failed to bind service")
			// We don't need to restage, because syslog service bindings don't change the app's environment variables

			logs = cf.Cf("logs", listenerAppName)
			randomMessage := random_name.CATSRandomName("RANDOM-MESSAGE")
			go writeLogsUntilInterrupted(interrupt, randomMessage, logWriterAppName)

			Eventually(logs, DEFAULT_TIMEOUT+1*time.Minute).Should(Say(randomMessage))
		})
	})
})

func getSyslogDrainAddress(appName string) string {
	var address []byte

	Eventually(func() []byte {
		re, err := regexp.Compile("ADDRESS: \\|(.*)\\|")
		Expect(err).NotTo(HaveOccurred())

		logs := cf.Cf("logs", appName, "--recent").Wait(DEFAULT_TIMEOUT)
		address = re.FindSubmatch(logs.Out.Contents())[1]
		return address
	}, DEFAULT_TIMEOUT).Should(Not(BeNil()))

	return string(address)
}

func writeLogsUntilInterrupted(interrupt chan struct{}, randomMessage string, logWriterAppName string) {
	defer GinkgoRecover()
	for {
		select {
		case <-interrupt:
			return
		default:
			helpers.CurlAppWithTimeout(logWriterAppName, "/log/"+randomMessage, DEFAULT_TIMEOUT)
			time.Sleep(3 * time.Second)
		}
	}
}
