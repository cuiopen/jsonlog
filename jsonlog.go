package jsonlog

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

type SwitchMode int

const (
	SWITCH_BY_DAY   SwitchMode = 1 // 按天切换文件
	SWITCH_BY_HOURS SwitchMode = 2 // 按小时切换文件
)

type M map[string]interface{}

type flushWriter interface {
	Write(data []byte) (n int, err error)
	Flush() error
}

// 日志记录器
type L struct {
	dir       string
	logChan   chan M
	closeChan chan int
	closeWait sync.WaitGroup
	out       flushWriter
	encoder   *json.Encoder
	file      *os.File
}

// 新建一个日志记录器
func New(dir string, switchMode SwitchMode, fileType string, compress bool) (*L, error) {
	// 目录不存在就创建一个
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			if err := os.Mkdir(dir, 0755); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}

	if compress {
		fileType += ".gz"
	}

	logger := &L{
		dir:       dir,
		closeChan: make(chan int),
		logChan:   make(chan M, 1000),
	}
	if err := logger.switchFile(switchMode, fileType, compress); err != nil {
		return nil, err
	}

	logger.closeWait.Add(1)
	go func() {
		var (
			fileTimer *time.Timer
			now       = time.Now()
		)
		switch switchMode {
		case SWITCH_BY_DAY:
			// 计算此刻到第二天零点的时间
			fileTimer = time.NewTimer(time.Date(
				now.Year(), now.Month(), now.Day(),
				0, 0, 0, 0, now.Location(),
			).Add(24 * time.Hour).Sub(now))
		case SWITCH_BY_HOURS:
			// 计算此刻到下一个小时的时间
			fileTimer = time.NewTimer(time.Date(
				now.Year(), now.Month(), now.Day(),
				now.Hour(), 0, 0, 0, now.Location(),
			).Add(time.Hour).Sub(now))
		}

		// 每两秒刷新一次
		flushTicker := time.NewTicker(2 * time.Second)
		defer func() {
			flushTicker.Stop()
			logger.out.Flush()
			logger.file.Close()
			logger.closeWait.Done()
		}()

		for {
			select {
			case r := <-logger.logChan:
				if err := logger.encoder.Encode(r); err != nil {
					log.Println("log failed:", err.Error())
				}
			case <-flushTicker.C:
				logger.out.Flush()
			case <-fileTimer.C:
				if err := logger.switchFile(switchMode, fileType, compress); err != nil {
					panic(err)
				}
				switch switchMode {
				case SWITCH_BY_DAY:
					fileTimer = time.NewTimer(24 * time.Hour)
				case SWITCH_BY_HOURS:
					fileTimer = time.NewTimer(time.Hour)
				}
			case <-logger.closeChan:
				return
			}
		}
	}()

	return logger, nil
}

// 切换文件
func (logger *L) switchFile(switchMode SwitchMode, fileType string, compress bool) error {
	var (
		dirName  string
		fileName string
		now      = time.Now()
	)

	// 确定目录名和文件名
	switch switchMode {
	case SWITCH_BY_DAY:
		dirName = logger.dir + "/" + now.Format("2006-01") + "/"
		fileName = dirName + fmt.Sprintf("%02d", now.Day()) + fileType
	case SWITCH_BY_HOURS:
		dirName = logger.dir + "/" + now.Format("2006-01-02") + "/"
		fileName = dirName + fmt.Sprintf("%02d", now.Hour()) + fileType
	}

	// 确认目录存在，否则就创建一个
	if _, err := os.Stat(dirName); err != nil {
		if os.IsNotExist(err) {
			if err := os.Mkdir(dirName, 0755); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	// 创建或者打开已存在文件
	file, err := os.OpenFile(fileName, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0755)
	if err != nil {
		return err
	}

	// 先关闭旧文件再切换
	if logger.file != nil {
		if err := logger.out.Flush(); err != nil {
			return err
		}
		if err := logger.file.Close(); err != nil {
			return err
		}
	}
	logger.file = file
	if compress {
		logger.out, _ = gzip.NewWriterLevel(bufio.NewWriter(logger.file), 9)
	} else {
		logger.out = bufio.NewWriter(logger.file)
	}
	logger.encoder = json.NewEncoder(logger.out)

	return nil
}

// 关闭日志系统
func (logger *L) Close() {
	close(logger.closeChan)
	logger.closeWait.Wait()
}

// 在日志文件中输出信息
func (logger *L) Log(r M) {
	logger.logChan <- r
}
