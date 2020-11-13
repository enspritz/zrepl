package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/fatih/color"
	"github.com/pkg/errors"

	"github.com/zrepl/zrepl/daemon/logging/trace"

	"github.com/zrepl/zrepl/config"
	"github.com/zrepl/zrepl/daemon/logging"
	"github.com/zrepl/zrepl/logger"
	"github.com/zrepl/zrepl/platformtest"
	"github.com/zrepl/zrepl/platformtest/tests"
)

var bold = color.New(color.Bold)
var boldRed = color.New(color.Bold, color.FgHiRed)
var boldGreen = color.New(color.Bold, color.FgHiGreen)

const DefaultPoolImageSize = 200 * (1 << 20)

func main() {

	var args HarnessArgs

	flag.StringVar(&args.CreateArgs.PoolName, "poolname", "", "")
	flag.StringVar(&args.CreateArgs.ImagePath, "imagepath", "", "")
	flag.Int64Var(&args.CreateArgs.ImageSize, "imagesize", DefaultPoolImageSize, "")
	flag.StringVar(&args.CreateArgs.Mountpoint, "mountpoint", "", "")
	flag.BoolVar(&args.StopAndKeepPoolOnFail, "failure.stop-and-keep-pool", false, "if a test case fails, stop test execution and keep pool as it was when the test failed")
	flag.StringVar(&args.Run, "run", "", "")
	flag.Parse()

	if err := HarnessRun(args); err != nil {
		os.Exit(1)
	}
}

var exitWithErr = fmt.Errorf("exit with error")

type HarnessArgs struct {
	CreateArgs            platformtest.ZpoolCreateArgs
	StopAndKeepPoolOnFail bool
	Run                   string
}

func HarnessRun(args HarnessArgs) error {

	runRE := regexp.MustCompile(args.Run)

	outlets := logger.NewOutlets()
	outlet, level, err := logging.ParseOutlet(config.LoggingOutletEnum{Ret: &config.StdoutLoggingOutlet{
		LoggingOutletCommon: config.LoggingOutletCommon{
			Level:  "debug",
			Format: "human",
		},
	}})
	if err != nil {
		panic(err)
	}
	outlets.Add(outlet, level)
	logger := logger.NewLogger(outlets, 1*time.Second)

	if err := args.CreateArgs.Validate(); err != nil {
		logger.Error(err.Error())
		panic(err)
	}
	ctx := context.Background()
	defer trace.WithTaskFromStackUpdateCtx(&ctx)()
	ctx = logging.WithLoggers(ctx, logging.SubsystemLoggersWithUniversalLogger(logger))
	ex := platformtest.NewEx(logger)

	type invocation struct {
		runFunc tests.Case
		result  *testCaseResult
	}

	invocations := make([]*invocation, 0, len(tests.Cases))
	for _, c := range tests.Cases {
		if runRE.MatchString(c.String()) {
			invocations = append(invocations, &invocation{runFunc: c})
		}
	}

	for _, inv := range invocations {

		bold.Printf("BEGIN TEST CASE %s\n", inv.runFunc.String())

		pool, err := platformtest.CreateOrReplaceZpool(ctx, ex, args.CreateArgs)
		if err != nil {
			panic(errors.Wrap(err, "create test pool"))
		}

		ctx := &platformtest.Context{
			Context:     ctx,
			RootDataset: filepath.Join(pool.Name(), "rootds"),
		}

		res := runTestCase(ctx, ex, inv.runFunc)
		inv.result = res
		if res.failed {
			fmt.Printf("%+v\n", res.failedStack) // print with stack trace
		}

		if res.failed && args.StopAndKeepPoolOnFail {
			boldRed.Printf("STOPPING TEST RUN AT FAILING TEST PER USER REQUEST\n")
			return exitWithErr
		}

		if err := pool.Destroy(ctx, ex); err != nil {
			panic(fmt.Sprintf("error destroying test pool: %s", err))
		}

		if res.failed {
			boldRed.Printf("TEST FAILED\n")
		} else if res.skipped {
			bold.Printf("TEST SKIPPED\n")
		} else if res.succeeded {
			boldGreen.Printf("TEST PASSED\n")
		} else {
			panic("unreachable")
		}

		fmt.Println()
	}

	var summary struct {
		succ, fail, skip []*invocation
	}
	for _, inv := range invocations {
		var bucket *[]*invocation
		if inv.result.failed {
			bucket = &summary.fail
		} else if inv.result.skipped {
			bucket = &summary.skip
		} else if inv.result.succeeded {
			bucket = &summary.succ
		} else {
			panic("unreachable")
		}
		*bucket = append(*bucket, inv)
	}
	printBucket := func(bucketName string, c *color.Color, bucket []*invocation) {
		c.Printf("%s:", bucketName)
		if len(bucket) == 0 {
			fmt.Printf(" []\n")
			return
		}
		fmt.Printf("\n")
		for _, inv := range bucket {
			fmt.Printf("  %s\n", inv.runFunc.String())
		}
	}
	printBucket("PASSING TESTS", boldGreen, summary.succ)
	printBucket("SKIPPED TESTS", bold, summary.skip)
	printBucket("FAILED TESTS", boldRed, summary.fail)

	if len(summary.fail) > 0 {
		return errors.New("at least one test failed")
	}
	return nil
}

type testCaseResult struct {
	// oneof
	failed, skipped, succeeded bool

	failedStack error // has stack inside, valid if failed=true
}

func runTestCase(ctx *platformtest.Context, ex platformtest.Execer, c tests.Case) *testCaseResult {

	// run case
	var panicked = false
	var panicValue interface{} = nil
	var panicStack error
	func() {
		defer func() {
			if item := recover(); item != nil {
				panicValue = item
				panicked = true
				panicStack = errors.Errorf("panic while running test: %v", panicValue)
			}
		}()
		c(ctx)
	}()

	if panicked {
		switch panicValue {
		case platformtest.SkipNowSentinel:
			return &testCaseResult{skipped: true}
		case platformtest.FailNowSentinel:
			return &testCaseResult{failed: true, failedStack: panicStack}
		default:
			return &testCaseResult{failed: true, failedStack: panicStack}
		}
	} else {
		return &testCaseResult{succeeded: true}
	}

}
