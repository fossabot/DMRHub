package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/go-co-op/gocron"
	gorm_seeder "github.com/kachit/gorm-seeder"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/USA-RedDragon/DMRHub/internal/config"
	"github.com/USA-RedDragon/DMRHub/internal/dmr"
	"github.com/USA-RedDragon/DMRHub/internal/http"
	"github.com/USA-RedDragon/DMRHub/internal/models"
	"github.com/USA-RedDragon/DMRHub/internal/repeaterdb"
	"github.com/USA-RedDragon/DMRHub/internal/sdk"
	"github.com/USA-RedDragon/DMRHub/internal/userdb"
	_ "github.com/tinylib/msgp/printer"
	"github.com/uptrace/opentelemetry-go-extra/otelgorm"
	"k8s.io/klog/v2"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var scheduler = gocron.NewScheduler(time.UTC)

func initTracer() func(context.Context) error {
	exporter, err := otlptrace.New(
		context.Background(),
		otlptracegrpc.NewClient(
			otlptracegrpc.WithInsecure(),
			otlptracegrpc.WithEndpoint(config.GetConfig().OTLPEndpoint),
		),
	)
	if err != nil {
		klog.Fatal(err)
	}
	resources, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			attribute.String("service.name", "DMRHub"),
			attribute.String("library.language", "go"),
		),
	)
	if err != nil {
		klog.Infof("Could not set resources: ", err)
	}

	otel.SetTracerProvider(
		sdktrace.NewTracerProvider(
			sdktrace.WithSampler(sdktrace.AlwaysSample()),
			sdktrace.WithBatcher(exporter),
			sdktrace.WithResource(resources),
		),
	)
	return exporter.Shutdown
}

func main() {
	defer klog.Flush()

	klog.Infof("DMRHub v%s-%s", sdk.Version, sdk.GitCommit)

	ctx := context.Background()

	if config.GetConfig().OTLPEndpoint != "" {
		cleanup := initTracer()
		defer func() {
			err := cleanup(ctx)
			if err != nil {
				klog.Errorf("Failed to shutdown tracer: %s", err)
			}
		}()
	}

	db, err := gorm.Open(postgres.Open(config.GetConfig().PostgresDSN), &gorm.Config{})
	if err != nil {
		klog.Exitf("Failed to open database: %s", err)
		return
	}
	if config.GetConfig().OTLPEndpoint != "" {
		if err = db.Use(otelgorm.NewPlugin()); err != nil {
			klog.Exitf("Failed to trace database: %s", err)
			return
		}
	}

	err = db.AutoMigrate(&models.AppSettings{})
	if err != nil {
		klog.Exitf("Failed to migrate database: %s", err)
		return
	}
	if db.Error != nil {
		//We have an error
		klog.Exitf(fmt.Sprintf("Failed with error %s", db.Error))
		return
	}

InitDB:
	// Grab the first (and only) AppSettings record. If that record doesn't exist, create it.
	var appSettings models.AppSettings
	result := db.First(&appSettings)
	if result.Error != nil {
		// We have an error
		if errors.Is(result.Error, gorm.ErrRecordNotFound) {
			if config.GetConfig().Debug {
				klog.Infof("App settings entry doesn't exist, migrating db and creating it")
			}
			// The record doesn't exist, so create it
			appSettings = models.AppSettings{
				HasSeeded: false,
			}
			err = db.AutoMigrate(&models.Call{}, &models.Repeater{}, &models.Talkgroup{}, &models.User{})
			if err != nil {
				klog.Exitf("Failed to migrate database: %s", err)
				return
			}
			if db.Error != nil {
				//We have an error
				klog.Exitf(fmt.Sprintf("Failed with error %s", db.Error))
				return
			}
			db.Create(&appSettings)
			if config.GetConfig().Debug {
				klog.Infof("App settings saved")
			}
		} else if strings.HasPrefix(result.Error.Error(), "ERROR: relation \"app_settings\" does not exist") {
			if config.GetConfig().Debug {
				klog.Infof("App settings table doesn't exist, creating it")
			}
			err = db.AutoMigrate(&models.AppSettings{})
			if err != nil {
				klog.Exitf("Failed to migrate database with AppSettings: %s", err)
				return
			}
			if db.Error != nil {
				//We have an error
				klog.Exitf(fmt.Sprintf("Failed to migrate database with AppSettings: %s", db.Error))
				return
			}
			goto InitDB
		} else {
			// We have an error
			klog.Exitf(fmt.Sprintf("App settings save failed with error %s", result.Error))
			return
		}
	}

	// If the record exists and HasSeeded is true, then we don't need to seed the database.
	if !appSettings.HasSeeded {
		usersSeeder := models.NewUsersSeeder(gorm_seeder.SeederConfiguration{Rows: 2})
		talkgroupsSeeder := models.NewTalkgroupsSeeder(gorm_seeder.SeederConfiguration{Rows: 1})
		seedersStack := gorm_seeder.NewSeedersStack(db)
		seedersStack.AddSeeder(&usersSeeder)
		seedersStack.AddSeeder(&talkgroupsSeeder)

		//Apply seed
		err = seedersStack.Seed()
		if err != nil {
			klog.Exitf("Failed to seed database: %s", err)
			return
		}
		appSettings.HasSeeded = true
		db.Save(&appSettings)
	}

	sqlDB, err := db.DB()
	if err != nil {
		klog.Exitf("Failed to open database: %s", err)
		return
	}
	sqlDB.SetMaxIdleConns(runtime.GOMAXPROCS(0))
	sqlDB.SetMaxOpenConns(runtime.GOMAXPROCS(0) * 10)
	sqlDB.SetConnMaxIdleTime(10 * time.Minute)

	// Dummy call to get the data decoded into memory early
	go func() {
		repeaterdb.GetDMRRepeaters()
		err = repeaterdb.Update()
		if err != nil {
			klog.Errorf("Failed to update repeater database: %s using built in one", err)
		}
	}()
	_, err = scheduler.Every(1).Day().At("00:00").Do(func() {
		err = repeaterdb.Update()
		if err != nil {
			klog.Errorf("Failed to update repeater database: %s", err)
		}
	})
	if err != nil {
		klog.Errorf("Failed to schedule repeater update: %s", err)
	}

	go func() {
		userdb.GetDMRUsers()
		err = userdb.Update()
		if err != nil {
			klog.Errorf("Failed to update user database: %s using built in one", err)
		}
	}()
	_, err = scheduler.Every(1).Day().At("00:00").Do(func() {
		err = userdb.Update()
		if err != nil {
			klog.Errorf("Failed to update repeater database: %s", err)
		}
	})
	if err != nil {
		klog.Errorf("Failed to schedule user update: %s", err)
	}

	scheduler.StartAsync()

	redis := redis.NewClient(&redis.Options{
		Addr:            config.GetConfig().RedisHost,
		Password:        config.GetConfig().RedisPassword,
		PoolFIFO:        true,
		PoolSize:        runtime.GOMAXPROCS(0) * 10,
		MinIdleConns:    runtime.GOMAXPROCS(0),
		ConnMaxIdleTime: 10 * time.Minute,
	})
	_, err = redis.Ping(ctx).Result()
	if err != nil {
		klog.Errorf("Failed to connect to redis: %s", err)
		return
	}
	defer func() {
		err := redis.Close()
		if err != nil {
			klog.Errorf("Failed to close redis: %s", err)
		}
	}()
	if config.GetConfig().OTLPEndpoint != "" {
		if err := redisotel.InstrumentTracing(redis); err != nil {
			klog.Errorf("Failed to trace redis: %s", err)
			return
		}

		// Enable metrics instrumentation.
		if err := redisotel.InstrumentMetrics(redis); err != nil {
			klog.Errorf("Failed to instrument redis: %s", err)
			return
		}
	}

	dmrServer := dmr.MakeServer(db, redis)
	dmrServer.Listen(ctx)
	defer dmrServer.Stop(ctx)

	// For each repeater in the DB, start a gofunc to listen for calls
	repeaters := models.ListRepeaters(db)
	for _, repeater := range repeaters {
		go repeater.ListenForCalls(ctx, redis)
	}

	http.Start(db, redis)
}
