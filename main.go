package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"strconv"
	"time"

	"go.elastic.co/apm"
	"golang.org/x/sync/errgroup"

	"github.com/elastic/hey-apm/benchmark"
	"github.com/elastic/hey-apm/models"
	"github.com/elastic/hey-apm/worker"
)

func init() {
	apm.DefaultTracer.Close()
	rand.Seed(1000)
}

func main() {
	if err := Main(); err != nil {
		log.Fatal(err)
	}
}

func Main() error {
	signalC := make(chan os.Signal, 1)
	signal.Notify(signalC, os.Interrupt)
	input := parseFlags()
	if input.IsBenchmark {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			// Ctrl+C when running benchmarks causes them to be
			// aborted, as the results are not meaningful for
			// comparison.
			defer cancel()
			<-signalC
			log.Printf("Interrupt signal received, aborting benchmarks...")
		}()
		if err := benchmark.Run(ctx, input); err != nil {
			return err
		}
		return nil
	}

	stopChan := make(chan struct{})
	go func() {
		// Ctrl+C when running load generation gracefully stops the
		// workers and prints the statistics.
		defer close(stopChan)
		<-signalC
		log.Printf("Interrupt signal received, stopping load generator...")
	}()
	return runWorkers(input, stopChan)
}

func runWorkers(input models.Input, stop <-chan struct{}) error {
	g, ctx := errgroup.WithContext(context.Background())
	for i := 0; i < input.Instances; i++ {
		idx := i
		g.Go(func() error {
			randomDelay := time.Duration(rand.Intn(input.DelayMillis)) * time.Millisecond
			fmt.Println(fmt.Sprintf("--- Starting instance (%v) in %v milliseconds", idx, randomDelay))
			time.Sleep(randomDelay)
			_, err := worker.Run(ctx, input, "", stop)
			return err
		})
	}
	return g.Wait()
}

func parseFlags() models.Input {
	// run options
	runTimeout := flag.Duration("run", 30*time.Second, "stop run after this duration")
	flushTimeout := flag.Duration("flush", 10*time.Second, "wait timeout for agent flush")
	seed := flag.Int64("seed", time.Now().Unix(), "random seed")
	instances := flag.Int("instances", 1, "number of concurrent instances to create load (only if -bench is not passed)")
	delayMillis := flag.Int("delay", 1000, "max delay in milliseconds per worker to start (only if -bench is not passed)")

	// convenience for https://www.elastic.co/guide/en/apm/agent/go/current/configuration.html
	serviceName := os.Getenv("ELASTIC_APM_SERVICE_NAME")
	if serviceName == "" {
		serviceName = *flag.String("service-name", "hey-service", "service name") // ELASTIC_APM_SERVICE_NAME
	}
	// apm-server options
	apmServerSecret := flag.String("apm-secret", "", "apm server secret token") // ELASTIC_APM_SECRET_TOKEN
	apmServerAPIKey := flag.String("api-key", "", "APM API yey")
	apmServerUrl := flag.String("apm-url", "http://localhost:8200", "apm server url") // ELASTIC_APM_SERVER_URL

	elasticsearchUrl := flag.String("es-url", "http://localhost:9200", "elasticsearch url for reporting")
	elasticsearchAuth := flag.String("es-auth", "", "elasticsearch username:password reporting")

	apmElasticsearchUrl := flag.String("apm-es-url", "http://localhost:9200", "elasticsearch output host for apm-server under load")
	apmElasticsearchAuth := flag.String("apm-es-auth", "", "elasticsearch output username:password for apm-server under load")

	isBench := flag.Bool("bench", false, "execute a benchmark with fixed parameters")
	regressionMargin := flag.Float64("rm", 1.1, "margin of acceptable performance decrease to not consider a regression (only in combination with -bench)")
	regressionDays := flag.String("rd", "7", "number of days back to check for regressions (only in combination with -bench)")

	// payload options
	errorLimit := flag.Int("e", math.MaxInt64, "max errors to generate (only if -bench is not passed)")
	errorFrequency := flag.Duration("ef", 1*time.Nanosecond, "error frequency. "+
		"generate errors up to once in this duration (only if -bench is not passed)")
	errorFrameMaxLimit := flag.Int("ex", 10, "max error frames to per error (only if -bench is not passed)")
	errorFrameMinLimit := flag.Int("em", 0, "max error frames to per error (only if -bench is not passed)")
	spanMaxLimit := flag.Int("sx", 10, "max spans to per transaction (only if -bench is not passed)")
	spanMinLimit := flag.Int("sm", 1, "min spans to per transaction (only if -bench is not passed)")
	transactionLimit := flag.Int("t", math.MaxInt64, "max transactions to generate (only if -bench is not passed)")
	transactionFrequency := flag.Duration("tf", 1*time.Nanosecond, "transaction frequency. "+
		"generate transactions up to once in this duration (only if -bench is not passed)")
	flag.Parse()

	if *spanMaxLimit < *spanMinLimit {
		spanMaxLimit = spanMinLimit
	}

	rand.Seed(*seed)

	input := models.Input{
		IsBenchmark:          *isBench,
		ApmServerUrl:         *apmServerUrl,
		ApmServerSecret:      *apmServerSecret,
		APIKey:               *apmServerAPIKey,
		ElasticsearchUrl:     *elasticsearchUrl,
		ElasticsearchAuth:    *elasticsearchAuth,
		ApmElasticsearchUrl:  *apmElasticsearchUrl,
		ApmElasticsearchAuth: *apmElasticsearchAuth,
		ServiceName:          serviceName,
		RunTimeout:           *runTimeout,
		FlushTimeout:         *flushTimeout,
		Instances:            *instances,
		DelayMillis:          *delayMillis,
	}

	if *isBench {
		if _, err := strconv.Atoi(*regressionDays); err != nil {
			panic(err)
		}
		input.RegressionDays = *regressionDays
		input.RegressionMargin = *regressionMargin
		return input
	}

	input.TransactionFrequency = *transactionFrequency
	input.TransactionLimit = *transactionLimit
	input.SpanMaxLimit = *spanMaxLimit
	input.SpanMinLimit = *spanMinLimit
	input.ErrorFrequency = *errorFrequency
	input.ErrorLimit = *errorLimit
	input.ErrorFrameMaxLimit = *errorFrameMaxLimit
	input.ErrorFrameMinLimit = *errorFrameMinLimit

	return input
}
