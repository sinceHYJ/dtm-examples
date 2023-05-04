package main

import (
	"context"
	"fmt"
	"github.com/dtm-labs/client/dtmcli/dtmimp"
	"github.com/dtm-labs/client/dtmcli/logger"
	"github.com/dtm-labs/client/workflow"
	"github.com/dtm-labs/dtm-examples/busi"
	"github.com/dtm-labs/dtm-examples/dtmutil"
	"github.com/dtm-labs/dtm-examples/examples"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

func hintExit(msg string) {
	if msg != "" {
		fmt.Print(msg, "\n")
	}
	fmt.Printf("Usage: %s <command>\n\nCommand can be one of the following:\n\n", filepath.Base(os.Args[0]))
	fmt.Printf("%4s%-32srun a quick start example\n", "", "qs")
	for _, cmd := range examples.Commands {
		fmt.Printf("%4s%-32srun an example includes %s\n", "", cmd.Arg, strings.ReplaceAll(cmd.Arg, "_", " "))
	}
	os.Exit(0)
}
func main() {
	if len(os.Args) == 1 {
		hintExit("")
	}
	logger.InitLog("debug")
	busi.StoreHost = "en.dtm.pub"
	busi.BusiConf = dtmimp.DBConf{
		Driver:   "mysql",
		Host:     busi.StoreHost,
		Port:     3306,
		User:     "dtm",
		Password: "passwd123dtm",
	}
	busi.ResetXaData()
	app, gsvr := busi.Startup()
	examples.AddRoutes(app)
	time.Sleep(200 * time.Millisecond)
	cmd := os.Args[1]
	initProvider(context.Background(), "localhost:4316")

	if cmd == "qs" {
		go busi.RunHTTP(app)
		time.Sleep(200 * time.Millisecond)
		busi.QsMain()
	} else if examples.IsExists(cmd) {
		if strings.Contains(cmd, "grpc") { // init workflow base on command
			nossl := grpc.WithTransportCredentials(insecure.NewCredentials())
			workflow.InitGrpc(dtmutil.DefaultGrpcServer, busi.BusiGrpc, gsvr)
			conn1, err := grpc.Dial(busi.BusiGrpc, grpc.WithUnaryInterceptor(workflow.Interceptor), nossl)
			logger.FatalIfError(err)
			busi.BusiCli = busi.NewBusiClient(conn1)
		} else {
			workflow.InitHTTP(dtmutil.DefaultHTTPServer, busi.Busi+"/workflow/resume")
		}
		go busi.RunGrpc(gsvr)
		go busi.RunHTTP(app)
		time.Sleep(200 * time.Millisecond)
		examples.Call(cmd)
	} else {
		hintExit("unknown command: " + cmd)
	}
	select {}
}

func initProvider(ctx context.Context, otelEndpoint string) (func(context.Context) error, error) {
	res, err := resource.New(ctx,
		resource.WithAttributes(
			// the service name used to display traces in backends
			semconv.ServiceName("dtm_examples"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	exporter, err := newExporter(ctx, otelEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Register the trace exporter with a TracerProvider, using a batch
	// span processor to aggregate spans before export.
	bsp := sdktrace.NewBatchSpanProcessor(exporter)
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
		sdktrace.WithResource(res),
		sdktrace.WithSpanProcessor(bsp),
	)
	otel.SetTracerProvider(tracerProvider)

	// set global propagator to tracecontext (the default is no-op).
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Shutdown will flush any remaining spans and shut down the exporter.
	return tracerProvider.Shutdown, nil
}

func newExporter(ctx context.Context, otelEndpoint string) (sdktrace.SpanExporter, error) {
	if otelEndpoint == "" {
		exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			return nil, fmt.Errorf("creating stdout exporter: %w", err)
		}
		return exporter, nil
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, otelEndpoint,
		// Note the use of insecure transport here. TLS is recommended in production.
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC connection to collector: %w", err)
	}

	// Set up a trace exporter
	traceExporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		return nil, fmt.Errorf("failed to create trace exporter: %w", err)
	}
	return traceExporter, nil
}
