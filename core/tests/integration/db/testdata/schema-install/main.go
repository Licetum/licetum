package main

import (
 "context"
 "fmt"
 "os"
 "time"

 "github.com/licetum/licetum/core/config"
 "github.com/licetum/licetum/core/db"
)

func main() {
 uri:=os.Getenv("LICETUM_SCHEMA_URI")
 if uri=="" { panic("LICETUM_SCHEMA_URI is required") }
 ctx,cancel:=context.WithTimeout(context.Background(),2*time.Minute);defer cancel()
 pool,err:=db.Connect(ctx,config.Database{URI:uri,PoolSize:2});if err!=nil{panic(err)};defer pool.Close()
 if err:=db.MigrateUpTo(ctx,pool,db.LatestMigration());err!=nil{panic(err)}
 fmt.Printf("installed schema version %d\n",db.LatestMigration())
}
