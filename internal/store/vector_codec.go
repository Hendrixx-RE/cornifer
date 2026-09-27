package store

import (
	"context"
	"database/sql/driver"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pgvector/pgvector-go"
)

// pgvector-go v0.4.1 (the latest published version as of this writing) only
// implements database/sql's Scanner/Valuer for pgvector.Vector, not pgx v5's
// native Codec interface — so without this file, pgx has no idea how to put
// a pgvector.Vector on the wire for the `vector` column type: query
// parameters and pgx.CopyFrom rows silently serialize via reflection over
// Vector's unexported field instead of the pgvector wire format, corrupting
// the data (observed as Postgres rejecting the result with "vector cannot
// have more than 16000 dimensions"). vectorCodec adapts pgvector.Vector's
// existing EncodeBinary/DecodeBinary/String/Parse methods (which already
// speak the correct wire format) to pgtype.Codec, and registerVectorType
// wires it up per-connection once the "vector" extension's OID is known
// (pgvector.RegisterTypes-style setup, done by hand since this pgvector-go
// version doesn't ship it).
type vectorCodec struct{}

func (vectorCodec) FormatSupported(format int16) bool {
	return format == pgtype.BinaryFormatCode || format == pgtype.TextFormatCode
}

func (vectorCodec) PreferredFormat() int16 { return pgtype.BinaryFormatCode }

func (vectorCodec) PlanEncode(_ *pgtype.Map, _ uint32, format int16, value any) pgtype.EncodePlan {
	if _, ok := value.(pgvector.Vector); !ok {
		return nil
	}
	switch format {
	case pgtype.BinaryFormatCode:
		return encodePlanVectorBinary{}
	case pgtype.TextFormatCode:
		return encodePlanVectorText{}
	default:
		return nil
	}
}

type encodePlanVectorBinary struct{}

func (encodePlanVectorBinary) Encode(value any, buf []byte) ([]byte, error) {
	return value.(pgvector.Vector).EncodeBinary(buf)
}

type encodePlanVectorText struct{}

func (encodePlanVectorText) Encode(value any, buf []byte) ([]byte, error) {
	return append(buf, value.(pgvector.Vector).String()...), nil
}

func (vectorCodec) PlanScan(_ *pgtype.Map, _ uint32, format int16, target any) pgtype.ScanPlan {
	if _, ok := target.(*pgvector.Vector); !ok {
		return nil
	}
	switch format {
	case pgtype.BinaryFormatCode:
		return scanPlanVectorBinary{}
	case pgtype.TextFormatCode:
		return scanPlanVectorText{}
	default:
		return nil
	}
}

type scanPlanVectorBinary struct{}

func (scanPlanVectorBinary) Scan(src []byte, dst any) error {
	if src == nil {
		*dst.(*pgvector.Vector) = pgvector.Vector{}
		return nil
	}
	return dst.(*pgvector.Vector).DecodeBinary(src)
}

type scanPlanVectorText struct{}

func (scanPlanVectorText) Scan(src []byte, dst any) error {
	if src == nil {
		*dst.(*pgvector.Vector) = pgvector.Vector{}
		return nil
	}
	return dst.(*pgvector.Vector).Parse(string(src))
}

func (c vectorCodec) DecodeValue(_ *pgtype.Map, _ uint32, format int16, src []byte) (any, error) {
	if src == nil {
		return nil, nil
	}
	var v pgvector.Vector
	var err error
	if format == pgtype.BinaryFormatCode {
		err = v.DecodeBinary(src)
	} else {
		err = v.Parse(string(src))
	}
	if err != nil {
		return nil, err
	}
	return v, nil
}

func (c vectorCodec) DecodeDatabaseSQLValue(m *pgtype.Map, oid uint32, format int16, src []byte) (driver.Value, error) {
	v, err := c.DecodeValue(m, oid, format, src)
	if err != nil || v == nil {
		return nil, err
	}
	return v.(pgvector.Vector).String(), nil
}

// registerVectorType looks up the OID pgvector's "vector" extension type
// was created with in this database (CREATE EXTENSION assigns it
// dynamically, so it can't be hardcoded) and registers vectorCodec for it
// on conn's type map. Called from pgxpool's AfterConnect hook so every
// pooled connection can encode/decode pgvector.Vector natively.
func registerVectorType(ctx context.Context, conn *pgx.Conn) error {
	var oid uint32
	err := conn.QueryRow(ctx, `SELECT oid FROM pg_type WHERE typname = 'vector'`).Scan(&oid)
	if err != nil {
		return fmt.Errorf("look up pgvector's vector type oid (is `CREATE EXTENSION vector` applied? see migrations/00001_create_repos.sql): %w", err)
	}
	conn.TypeMap().RegisterType(&pgtype.Type{Name: "vector", OID: oid, Codec: vectorCodec{}})
	return nil
}
