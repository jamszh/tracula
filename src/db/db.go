package db

import (
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"log"
	"playercount/src/env"
	"playercount/src/stats" // Way to clear this?
	"time"
)

// DB Constants
const (
	DBTIMEOUT   = 10
	DATEPATTERN = "2006-01-02 15:04:05"
)

// App - Entry in the DB is of this format
type App struct {
	ID           primitive.ObjectID  `bson:"_id,omitempty"`
	Name         string              `bson:"name"`
	AppID        int                 `bson:"app_id"`
	Metrics      []Metric            `bson:"metrics"`
	DailyMetrics []stats.DailyMetric `bson:"daily_metrics"`
	Domain       string              `bson:"domain"`
}

// Metric element
type Metric struct {
	Date        time.Time `bson:"date"`
	AvgPlayers  int       `bson:"avg_players"`
	Gain        string    `bson:"gain"`
	GainPercent string    `bson:"gain_percent"`
	Peak        int       `bson:"peak_players"`
}

type dbParams struct {
	ctx    context.Context
	db     *mongo.Database
	client *mongo.Client
}

type collections struct {
	stats      *mongo.Collection
	exceptions *mongo.Collection
}

var param dbParams = initEnvironmentParams()
var cols = collections{
	stats:      param.db.Collection("population_stats"),
	exceptions: param.db.Collection("exceptions"),
}

func initEnvironmentParams() dbParams {

	var err error
	var newDb *mongo.Database
	var newClient *mongo.Client

	tmpURI := env.GoDotEnvVariable("DEV_URI")
	newClient, err = mongo.NewClient(options.Client().ApplyURI(tmpURI))
	if err != nil {
		log.Fatalf("[CRITICAL] Error initialising client. URI: %s", tmpURI)
	}

	// Dependent environemnt params
	if env.GoDotEnvVariable("NODE_ENV") == "dev" {
		newDb = newClient.Database("games_stats_app_TST")
		fmt.Printf("Target: TST DB\n")
	} else {
		newDb = newClient.Database("games_stats_app")
		fmt.Printf("Target: PRD DB\n")
	}

	// Independent environment params
	newCtx, cancelFunc := context.WithTimeout(context.Background(), DBTIMEOUT*time.Second)
	defer cancelFunc()

	err = newClient.Connect(newCtx)
	if err != nil {
		log.Fatalf("[CRITICAL] Error connecting client. %s", err)
	}

	var newDbParams = dbParams{
		ctx:    context.Background(), // Why can't I use newCtx here?
		db:     newDb,
		client: newClient,
	}

	return newDbParams
}

type dbAppProjection struct {
	ID       int `bson:"_id"`
	Name     int `bson:"name"`
	Domain   int `bson:"domain"`
	DomainID int `bson:"app_id"`
}

type dbAppRef struct {
	ID       primitive.ObjectID `bson:"_id"`
	Name     string             `bson:"name"`
	Domain   string             `bson:"domain"`
	DomainID int                `bson:"app_id"`
}

// AppRef : app data (no historical data)
type AppRef struct {
	Date time.Time `bson:"date"`
	Ref  dbAppRef  `bson:"reference"`
}

// GetAppList : Get List of Apps as AppMeta
func GetAppList() []AppRef {

	// Empty filter - searching for all elements
	filter := bson.M{}

	var cursor *mongo.Cursor

	// Define projection
	proj := dbAppProjection{
		ID:       1,
		Name:     1,
		Domain:   1,
		DomainID: 1,
	}

	// Query options
	// Only want fields corresponding to dbAppRef
	opts := options.Find().SetProjection(proj)
	cursor, err := cols.stats.Find(param.ctx, filter, opts)
	if err != nil {
		log.Fatal(err)
	}

	dateTime, err := time.Parse(DATEPATTERN, time.Now().UTC().String()[:19])
	if err != nil {
		log.Fatal(err)
	}

	var appList []AppRef

	for cursor.Next(param.ctx) {
		var dbEntry dbAppRef

		if err := cursor.Decode(&dbEntry); err != nil {
			log.Printf("Error decoding DB entry. %s", err)
			continue
		}

		aNewMetaElement := AppRef{
			Ref:  dbEntry,
			Date: dateTime,
		}

		appList = append(appList, aNewMetaElement)
	}
	cursor.Close(param.ctx)

	return appList
}

// InsertDaily : Insert daily metric
func InsertDaily(newDaily *stats.DailyMetric, app *AppRef) error {
	log.Printf("[PlayerCount Collection] storing %+v to DB for app %d", *newDaily, app.Ref.DomainID)

	match := bson.M{"_id": app.Ref.ID}
	action := bson.M{"$push": bson.M{"daily_metrics": newDaily}}
	_, err := cols.stats.UpdateOne(param.ctx, match, action)
	if err != nil {
		return err
	}
	log.Println("[PlayerCount Collection] new daily metric successfully inserted")
	return nil
}

// InsertException : Insert exception instance
func InsertException(app *AppRef) error {
	log.Printf("[Exception Queue] inserting daily update for app %s [%s]: %s \n",
		app.Ref.Name, app.Ref.ID.String(), app.Date.String())

	res, err := cols.exceptions.InsertOne(param.ctx, app)
	if err != nil {
		return err
	}
	log.Printf("Added to exception queue %s", res)
	return nil
}

// GetExceptions - Return list of AppRefs and clear collection
func GetExceptions() (*[]AppRef, error) {
	var appRefs []AppRef

	cursor, err := cols.exceptions.Find(param.ctx, bson.M{})
	if err != nil {
		log.Printf("Error processing exceptions. %s", err)
		return nil, err
	}

	if err = cursor.All(param.ctx, &appRefs); err != nil {
		log.Printf("Error assembling excpetions. %s", err)
		return nil, err
	}

	return &appRefs, nil
}
