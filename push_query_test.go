package ksqldbx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tamboto2000/ksqldbx/internal/utils"
	"github.com/tamboto2000/ksqldbx/net"
)

const (
	createStreamStmnt = `
	CREATE STREAM IF NOT EXISTS test_stream(
		d1 BOOLEAN,
		d2 VARCHAR,
		d3 INT,
		d4 BIGINT,
		d5 DOUBLE,
		d6 DECIMAL(5, 2)
	) WITH (
		kafka_topic='test_stream', 
		value_format='protobuf',
		partitions=1
	);
`
	insertStreamQuery = `
		INSERT INTO test_stream(
			d1,
			d2,
			d3,
			d4,
			d5,
			d6			
		) VALUES (
			true,
			'varchar',
			1,
			10,
			1.5,
			10.50
		);
	`
)

func TestIntegrationKsqlDB_Push(t *testing.T) {
	streamName := "push_test"

	ksql, err := NewKsqlDB(net.Options{
		BaseUrl:   "http://localhost:8088",
		AllowHTTP: true,
	})

	require.Nil(t, err)

	// create a stream
	_, err = ksql.Exec(context.Background(), StmntSQL{
		KSQL: utils.CreateStreamTestStmnt(streamName),
	})

	require.Nil(t, err)

	type args struct {
		q        QuerySQL
		headChan chan Header
		rowChan  chan Row
	}
	type test struct {
		name       string
		args       args
		initData   func(ctx context.Context, ksql *KsqlDB) error
		wantHeader Header
		wantRows   []Row
		wantErr    bool
	}
	tests := []test{
		{
			name: "success running SELECT query",
			args: args{
				q: QuerySQL{
					SQL: "SELECT * FROM ${stream_name} EMIT CHANGES LIMIT 1;",
					Properties: Properties{
						"auto.offset.reset": "earliest",
					},
					Variables: Variables{
						"stream_name": streamName,
					},
				},
				headChan: make(chan Header),
				rowChan:  make(chan Row),
			},
			initData: func(ctx context.Context, ksql *KsqlDB) error {
				// insert exactly one row of data
				stmnt := StmntSQL{
					KSQL: insertStreamQuery,
				}

				_, err := ksql.Exec(ctx, stmnt)
				return err
			},
			wantHeader: Header{
				ColumnNames: []string{"D1", "D2", "D3", "D4", "D5", "D6"},
				ColumnTypes: []string{"BOOLEAN", "STRING", "INTEGER", "BIGINT", "DOUBLE", "DECIMAL(5, 2)"},
			},
			wantRows: []Row{
				// by default data with data type INTEGER (or any integer for that matter)
				// will be decoded into type of float64
				{true, "varchar", float64(1), float64(10), 1.5, 10.50},
			},
			wantErr: false,
		},
		{
			name: "fail running invalid SELECT query",
			args: args{
				q: QuerySQL{
					SQL: "SELECT * blabla;",
				},
				headChan: make(chan Header),
				rowChan:  make(chan Row),
			},
			initData: func(ctx context.Context, ksql *KsqlDB) error {
				return nil
			},
			wantHeader: Header{},
			wantRows:   []Row{},
			wantErr:    true,
		},
	}
	for _, tt := range tests {
		ctx, cancel := context.WithCancel(context.Background())

		err := tt.initData(ctx, ksql)
		require.Nil(t, err)

		go func(tt test) {
			timer := time.NewTimer(3 * time.Second)
			var rowCount int
			for _, expectRow := range tt.wantRows {
				select {
				case <-timer.C:
					assert.Equalf(t, len(tt.wantRows), rowCount, "expected row count: %d, got row count: %d", len(tt.wantRows), rowCount)
					cancel()
					return
				case header := <-tt.args.headChan:
					assert.Equal(t, tt.wantHeader.ColumnNames, header.ColumnNames)
					assert.Equal(t, tt.wantHeader.ColumnTypes, header.ColumnTypes)

				case row := <-tt.args.rowChan:
					assert.Equal(t, expectRow, row)
					timer.Reset(3 * time.Second)
					rowCount++
				}
			}

			cancel()
			if !timer.Stop() {
				<-timer.C
			}
		}(tt)

		err = ksql.Push(ctx, tt.args.q, tt.args.headChan, tt.args.rowChan)
		if tt.wantErr {
			assert.NotNil(t, err)
		} else {
			assert.Nil(t, err)
		}
	}
}
