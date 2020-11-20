package snippets

import (
	"crypto/sha512"
	"errors"
	"fmt"
	"github.com/GalvinGao/mirai-group-snippets/config"
	"github.com/davecgh/go-spew/spew"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"io/ioutil"
	"math/rand"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"

	"github.com/GalvinGao/mirai-group-snippets/bot"
	"github.com/GalvinGao/mirai-group-snippets/utils"
)

const ModuleID = "galvingao.snippets"

type CommandType int

const (
	CommandTypeAddRecord CommandType = iota + 1
	CommandTypeRandomRecord

	CommandTypeDoNothing
)

func intContains(s []int, e int) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}

func init() {
	instance = &snippet{}
	bot.RegisterModule(instance)
}

type snippet struct {
	groups []int
	db     *gorm.DB
}

type Snippet struct {
	gorm.Model

	FromUserUin int64
	FromUserDisplay string
	FromGroup   int64

	ImagePath string
}

func (m *snippet) MiraiGoModule() bot.ModuleInfo {
	return bot.ModuleInfo{
		ID:       ModuleID,
		Instance: instance,
	}
}

func (m *snippet) Init() {
	// 初始化过程
	// 在此处可以进行 Module 的初始化配置
	// 如配置读取
	m.groups = config.GlobalConfig.GetIntSlice("snippets.groups")
}

func (m *snippet) PostInit() {
	// 第二次初始化
	// 再次过程中可以进行跨Module的动作
	// 如通用数据库等等
	dsn := config.GlobalConfig.GetString("database.dsn")
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		logger.Errorln("failed to initialize database", err)
	}
	m.db = db

	m.db.AutoMigrate(&Snippet{})
}

func (m *snippet) Serve(b *bot.Bot) {
	// 注册服务函数部分
	b.OnGroupMessage(func(qqClient *client.QQClient, groupMessage *message.GroupMessage) {
		if intContains(m.groups, int(groupMessage.GroupCode)) {
			m.dispatcher(qqClient, groupMessage)
		}
	})
}

func (m *snippet) Start(b *bot.Bot) {
	// 此函数会新开携程进行调用
	// ```go
	// 		go exampleModule.Start()
	// ```

	// 可以利用此部分进行后台操作
	// 如http服务器等等
}

func (m *snippet) Stop(b *bot.Bot, wg *sync.WaitGroup) {
	// 别忘了解锁
	defer wg.Done()
	// 结束部分
	// 一般调用此函数时，程序接收到 os.Interrupt 信号
	// 即将退出
	// 在此处应该释放相应的资源或者对状态进行保存
	db, err := m.db.DB()
	if err != nil {
		logger.Errorln("cannot get db.db() when trying to Close() db", err)
	}
	err = db.Close()
	if err != nil {
		logger.Errorln("failed to Close() database", err)
	}
}

var instance *snippet

var logger = utils.GetModuleLogger(ModuleID)

func (m *snippet) sendRandomSnippet() (*Snippet, error) {
	//client.SendGroupMessage()
	var count int64
	err := m.db.Model(&Snippet{}).Count(&count).Error
	if err != nil {
		return nil, err
	}
	pivot := rand.Int63n(count + 1)
	var found Snippet
	err = m.db.Model(&Snippet{}).Offset(int(pivot)).Limit(1).First(&found).Error
	if err != nil {
		return nil, err
	}
	return &found, err
}

func (m *snippet) recordSnippet(msg *message.GroupMessage, img []byte) (*Snippet, error) {
	shasum := sha512.Sum512(img)
	imgPath := path.Join("images", string(shasum[:]))

	err := ioutil.WriteFile(imgPath, img, 0644)
	if err != nil {
		logger.Errorln("failed to write to file", err)
		return &Snippet{}, errors.New("[!] 语录添加失败：internal: failed to write image to file")
	}

	s := &Snippet{
		FromUserUin: msg.Sender.Uin,
		FromUserDisplay: msg.Sender.DisplayName(),
		FromGroup:   msg.GroupCode,
		ImagePath:   imgPath,
	}

	err = m.db.Create(s).Error
	if err != nil {
		logger.Errorln("failed to create db record", err)
		return &Snippet{}, errors.New("[!] 语录添加失败：internal: failed to create db record")
	}

	return s, nil
}

func (m *snippet) sendText(client *client.QQClient, groupId int64, text string) {
	sm := message.NewSendingMessage()
	sm.Append(message.NewText(text))
	client.SendGroupMessage(groupId, sm)
}

func (m *snippet) dispatcher(client *client.QQClient, msg *message.GroupMessage) {
	var command CommandType
	var imageData []byte

	for _, element := range msg.Elements {
		switch el := element.(type) {
		case *message.TextElement:
			cleaned := strings.Replace(strings.TrimSpace(el.Content), "！", "!", 1)
			if strings.HasPrefix(cleaned, "!添加语录") {
				command = CommandTypeAddRecord
				continue
			}
			if strings.HasPrefix(cleaned, "!随机语录") {
				command = CommandTypeRandomRecord
				continue
			}
			command = CommandTypeDoNothing
		case *message.ImageElement:
			imageData = el.Data
		}
	}

	switch command {
	case CommandTypeAddRecord:
		if imageData == nil {
			m.sendText(client, msg.GroupCode, "[!] 语录添加失败：no image found in message. please send image with the command in one message.")
			return
		}
		s, err := m.recordSnippet(msg, imageData)
		if err != nil {
			m.sendText(client, msg.GroupCode, err.Error())
			return
		}
		m.sendText(client, msg.GroupCode, fmt.Sprintf("[✓] 语录已添加为如下记录\n%s", spew.Sdump(s)))
	case CommandTypeRandomRecord:
		s, err := m.sendRandomSnippet()
		if err != nil {
			m.sendText(client, msg.GroupCode, "[!] 随机语录读取失败：" + err.Error())
			return
		}
		sm := message.NewSendingMessage()
		f, err := os.OpenFile(s.ImagePath, os.O_RDONLY, 0644)
		if err != nil {
			m.sendText(client, msg.GroupCode, "[!] 随机语录读取失败：" + err.Error())
			return
		}
		img, err := ioutil.ReadAll(f)
		if err != nil {
			m.sendText(client, msg.GroupCode, "[!] 随机语录读取失败：" + err.Error())
			return
		}

		sm.Append(message.NewText(fmt.Sprintf("由 %v (%v) 录入于群 %v 的随机语录 #%v：", s.FromUserDisplay, s.FromUserUin, s.FromGroup, s.ID)))
		sm.Append(message.NewImage(img))
		client.SendGroupMessage(msg.GroupCode, sm)
	}
}
