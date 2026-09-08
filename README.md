# tinydb-go

embedded key-value store in Go. append-only log, in-memory index, CRC32 per record, compaction.

## usage

```
go run main.go db.bin
> set name zatm
ok
> get name
zatm
> set role dev
ok
> list
name
role
(2 keys)
> del role
deleted
> compact db2.bin
compacted
> exit
```

or one-shot:

```
go run main.go db.bin set name zatm
go run main.go db.bin get name
```

## how it works

- append-only write-ahead log on disk
- each record: `[keyLen:2][valLen:4][crc:4][key][val][crc:4]`
- in-memory index maps key → file offset
- deletes are tombstone records (valLen = 0xFFFFFFFF)
- `compact` rewrites a fresh file with only live records
- CRC32 validates integrity on every read

## why

i wanted to understand how a real KV store works internally — not use one, build one. this is ~370 lines of Go, no external deps.
