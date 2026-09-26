package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"sort"
	"strings"
)

const (
	maxKey = 255
	maxVal = 1 << 20
	magic  = 0x54444231
)

type DB struct {
	file   *os.File
	index  map[string]uint64
	size   uint64
	closed bool
}

type record struct {
	keyLen uint16
	valLen uint32
	key    string
	val    []byte
}

func Open(path string) (*DB, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	db := &DB{
		file:  f,
		index: make(map[string]uint64),
	}

	if err := db.recover(); err != nil {
		f.Close()
		return nil, fmt.Errorf("recover: %w", err)
	}
	if err := f.Truncate(int64(db.size)); err != nil {
		f.Close()
		return nil, fmt.Errorf("truncate: %w", err)
	}

	return db, nil
}

func (db *DB) recover() error {
	stat, err := db.file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() < 16 {
		header := make([]byte, 16)
		binary.LittleEndian.PutUint32(header[0:4], magic)
		binary.LittleEndian.PutUint64(header[8:16], 0)
		if _, err := db.file.Write(header); err != nil {
			return err
		}
		db.size = 16
		return nil
	}

	header := make([]byte, 16)
	if _, err := db.file.ReadAt(header, 0); err != nil {
		return err
	}
	if binary.LittleEndian.Uint32(header[0:4]) != magic {
		return errors.New("bad magic")
	}

	offset := uint64(16)
	end := uint64(stat.Size())

	for offset < end {
		rec, n, err := db.readRecord(offset)
		if err == io.EOF || err == errPartial {
			break
		}
		if err != nil {
			return err
		}

		if rec.valLen == 0xFFFFFFFF {
			delete(db.index, rec.key)
		} else {
			db.index[rec.key] = offset
		}
		offset += n
	}

	db.size = offset
	return nil
}

var errPartial = errors.New("partial record")

func (db *DB) readRecord(offset uint64) (*record, uint64, error) {
	header := make([]byte, 10)
	n, err := db.file.ReadAt(header, int64(offset))
	if err != nil && err != io.EOF {
		return nil, 0, err
	}
	if n < 10 {
		return nil, 0, io.EOF
	}

	keyLen := binary.LittleEndian.Uint16(header[0:2])
	valLen := binary.LittleEndian.Uint32(header[2:6])
	crc := binary.LittleEndian.Uint32(header[6:10])
	deleted := valLen == 0xFFFFFFFF

	if keyLen == 0 || keyLen > maxKey || (!deleted && valLen > maxVal) {
		return nil, 0, errPartial
	}

	payloadLen := uint64(keyLen)
	if !deleted {
		payloadLen += uint64(valLen)
	}
	if payloadLen > ^uint64(0)-14 || offset > ^uint64(0)-(payloadLen+14) {
		return nil, 0, errPartial
	}
	total := payloadLen + 14

	data := make([]byte, payloadLen+4)
	n2, err := db.file.ReadAt(data, int64(offset+10))
	if err != nil && err != io.EOF {
		return nil, 0, err
	}
	if uint64(n2) < payloadLen+4 {
		return nil, 0, errPartial
	}

	key := string(data[:keyLen])
	var val []byte
	if !deleted {
		val = append([]byte(nil), data[keyLen:payloadLen]...)
	}
	storedCrc := binary.LittleEndian.Uint32(data[payloadLen : payloadLen+4])

	h := crc32.NewIEEE()
	h.Write(header[0:6])
	h.Write(data[:payloadLen])
	if h.Sum32() != crc || storedCrc != crc {
		return nil, 0, errPartial
	}

	return &record{keyLen: keyLen, valLen: valLen, key: key, val: val}, total, nil
}

func (db *DB) writeRecord(key string, val []byte) (uint64, error) {
	if len(key) == 0 {
		return 0, errors.New("key cannot be empty")
	}
	if len(key) > maxKey {
		return 0, fmt.Errorf("key too long (max %d)", maxKey)
	}
	if len(val) > maxVal {
		return 0, fmt.Errorf("value too long (max %d)", maxVal)
	}

	header := make([]byte, 10)
	binary.LittleEndian.PutUint16(header[0:2], uint16(len(key)))
	binary.LittleEndian.PutUint32(header[2:6], uint32(len(val)))

	h := crc32.NewIEEE()
	h.Write(header[0:6])
	h.Write([]byte(key))
	h.Write(val)
	crc := h.Sum32()
	binary.LittleEndian.PutUint32(header[6:10], crc)

	crcBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(crcBytes, crc)

	offset := db.size
	buf := append(append(append(header, []byte(key)...), val...), crcBytes...)

	if _, err := db.file.WriteAt(buf, int64(offset)); err != nil {
		return 0, err
	}
	db.size += uint64(len(buf))
	return offset, nil
}

func (db *DB) Set(key string, val []byte) error {
	offset, err := db.writeRecord(key, val)
	if err != nil {
		return err
	}
	if err := db.file.Sync(); err != nil {
		return err
	}
	db.index[key] = offset
	return nil
}

func (db *DB) Get(key string) ([]byte, error) {
	offset, ok := db.index[key]
	if !ok {
		return nil, errors.New("key not found")
	}
	rec, _, err := db.readRecord(offset)
	if err != nil {
		return nil, err
	}
	return rec.val, nil
}

func (db *DB) Delete(key string) error {
	if len(key) == 0 {
		return errors.New("key cannot be empty")
	}
	if len(key) > maxKey {
		return fmt.Errorf("key too long (max %d)", maxKey)
	}
	header := make([]byte, 10)
	binary.LittleEndian.PutUint16(header[0:2], uint16(len(key)))
	binary.LittleEndian.PutUint32(header[2:6], 0xFFFFFFFF)

	h := crc32.NewIEEE()
	h.Write(header[0:6])
	h.Write([]byte(key))
	crc := h.Sum32()
	binary.LittleEndian.PutUint32(header[6:10], crc)

	crcBytes := make([]byte, 4)
	binary.LittleEndian.PutUint32(crcBytes, crc)

	buf := append(append(header, []byte(key)...), crcBytes...)
	if _, err := db.file.WriteAt(buf, int64(db.size)); err != nil {
		return err
	}
	db.size += uint64(len(buf))
	if err := db.file.Sync(); err != nil {
		return err
	}
	delete(db.index, key)
	return nil
}

func (db *DB) Keys() []string {
	keys := make([]string, 0, len(db.index))
	for k := range db.index {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (db *DB) Close() error {
	if db.closed {
		return nil
	}
	db.closed = true
	return db.file.Close()
}

func (db *DB) Compact(path string) error {
	newFile, err := os.Create(path)
	if err != nil {
		return err
	}
	defer newFile.Close()

	header := make([]byte, 16)
	binary.LittleEndian.PutUint32(header[0:4], magic)
	if _, err := newFile.Write(header); err != nil {
		return err
	}

	keys := db.Keys()
	newDB := &DB{file: newFile, index: make(map[string]uint64), size: 16}

	for _, k := range keys {
		v, err := db.Get(k)
		if err != nil {
			continue
		}
		if _, err := newDB.writeRecord(k, v); err != nil {
			return err
		}
		newDB.index[k] = newDB.size - uint64(10+len(k)+len(v)+4)
	}

	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: tinydb <dbfile> [command] [args]")
		fmt.Fprintln(os.Stderr, "commands: set <k> <v>, get <k>, del <k>, list, compact <newfile>")
		os.Exit(1)
	}

	dbPath := os.Args[1]
	db, err := Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer db.Close()

	if len(os.Args) < 3 {
		scanner := bufio.NewScanner(os.Stdin)
		fmt.Print("> ")
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				fmt.Print("> ")
				continue
			}
			if !handleCmd(db, line) {
				break
			}
			fmt.Print("> ")
		}
	} else {
		cmd := strings.Join(os.Args[2:], " ")
		handleCmd(db, cmd)
	}
}

func handleCmd(db *DB, line string) bool {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return true
	}

	switch parts[0] {
	case "set":
		if len(parts) < 3 {
			fmt.Println("usage: set <key> <value>")
			return true
		}
		key := parts[1]
		val := strings.Join(parts[2:], " ")
		if err := db.Set(key, []byte(val)); err != nil {
			fmt.Println("error:", err)
		} else {
			fmt.Println("ok")
		}
	case "get":
		if len(parts) < 2 {
			fmt.Println("usage: get <key>")
			return true
		}
		val, err := db.Get(parts[1])
		if err != nil {
			fmt.Println("error:", err)
		} else {
			fmt.Println(string(val))
		}
	case "del":
		if len(parts) < 2 {
			fmt.Println("usage: del <key>")
			return true
		}
		if err := db.Delete(parts[1]); err != nil {
			fmt.Println("error:", err)
		} else {
			fmt.Println("deleted")
		}
	case "list":
		keys := db.Keys()
		for _, k := range keys {
			fmt.Println(k)
		}
		fmt.Printf("(%d keys)\n", len(keys))
	case "compact":
		if len(parts) < 2 {
			fmt.Println("usage: compact <newfile>")
			return true
		}
		if err := db.Compact(parts[1]); err != nil {
			fmt.Println("error:", err)
		} else {
			fmt.Println("compacted")
		}
	case "exit", "quit":
		return false
	default:
		fmt.Println("unknown command:", parts[0])
	}
	return true
}
