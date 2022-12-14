package main

import (
	"errors"
	"fmt"
	"io"
	"runtime"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/amdf/ixxatvci3"
	"github.com/amdf/ixxatvci3/candev"
)

var canOk = make(chan int)
var can *candev.Device
var b candev.Builder

var bOkCAN bool
var bConnected bool
var bServiceMode bool
var bfileSelect bool // файл прошивки выбран

var labelConnect = widget.NewLabel("")
var labelServiceMode = widget.NewLabel("")
var labelVersion = widget.NewLabel("")
var labelLoading = widget.NewLabel("")
var btnUpdate *widget.Button
var btnOpen *widget.Button
var ppbar *widget.ProgressBar
var ppbarValue binding.Float

var dataFirmware []byte

var stage = UNDEFINED  // этап обновления
var compliteStage bool // признак завершения этапа

func main() {
	a := app.New()
	a.Settings().SetTheme(theme.DarkTheme())
	w := a.NewWindow("Программа обновления ПО ИПТМ-395.1")
	w.Resize(fyne.NewSize(400, 400))
	w.SetFixedSize(true)
	w.CenterOnScreen()

	btnUpdate = widget.NewButton("Начать обновление", func() {
		stage = PREPARE_UPDATE
		btnUpdate.Disable()
		btnOpen.Disable()
	})

	btnOpen = widget.NewButton("Открыть файл", func() {
		fileDialog := dialog.NewFileOpen( //
			func(reader fyne.URIReadCloser, _ error) { // колбек,после выброра файла

				dataFirmware, _ = io.ReadAll(reader) // читаем данные из файла
				// nameFirmware := reader.URI().Path()// путь до выбранного файла
			},
			w, // передаем окно
		)
		fileDialog.Resize(fyne.NewSize(600, 600))
		fileDialog.SetFilter(
			storage.NewExtensionFileFilter([]string{".bin"})) // только файлы bin
		fileDialog.Show()
		bfileSelect = true
	})

	labelNull := widget.NewLabel("")

	ppbar = widget.NewProgressBar()
	ppbarValue = binding.NewFloat()
	ppbar.Bind(ppbarValue)
	ppbar.Min = 0
	ppbar.Max = 100
	ppbar.Hidden = true // спрятать до момента загрузки

	btnStartUpdate := container.New( // кнопкa по центру
		layout.NewHBoxLayout(),
		layout.NewSpacer(),
		btnUpdate,
		layout.NewSpacer(),
	)

	btnOpenFile := container.New(
		layout.NewHBoxLayout(),
		layout.NewSpacer(),
		btnOpen,
		layout.NewSpacer(),
	)

	TopPanel := container.NewVBox(
		labelConnect,
		labelServiceMode,
		labelVersion,
		btnOpenFile,
		labelNull,
		btnStartUpdate,
		labelNull,
	)

	BottomPanel := container.NewVBox( // вертикальное расположение

		ppbar,
		labelNull,
	)

	content := container.NewBorder(TopPanel, BottomPanel, labelLoading, nil) // расположение относительно центра

	w.SetContent(content)

	go connectCAN()
	go processCAN()
	go processScreen()
	go enterServiceMode()

	defer func() {
		bOkCAN = false
		resetInfo()
		can.Stop()
	}()

	w.ShowAndRun()
}

func resetInfo() {
	bConnected = false
	bServiceMode = false
}

func processScreen() {
	sec := time.NewTicker(200 * time.Millisecond)
	for range sec.C {

		stringConnected := ""
		stringService := ""
		stringVersion := ""

		if bOkCAN {
			if bConnected {
				stringConnected = "Соединено с ИПТМ-395"

				if bServiceMode {
					stringService = "Включен сервисный режим"
				} else {
					if !bServiceMode {
						stringService = "Ожидание перехода в сервисный режим..."
					}
				}

				if !bfileSelect && stage == UNDEFINED {
					labelLoading.SetText("Выберите файл прошивки")
				} else if bfileSelect && stage == UNDEFINED {
					labelLoading.SetText(" ")
				}

				if bConnected && bServiceMode && bfileSelect && stage == UNDEFINED {
					btnUpdate.Enable()
				} else {
					btnUpdate.Disable()
				}

				stringVersion = fmt.Sprintf("Версия: %2d.%2d.%2d", VER_MAJOR, VER_MINOR, VER_PATCH)

			} else {
				if stage == FINISH {
					stringConnected = ""
				} else {
					stringConnected = "Ожидание соединения с ИПТМ-395..."

				}
				btnUpdate.Disable()
			}
		} else {
			stringConnected = "Не обнаружен адаптер USB-to-CAN"
			btnUpdate.Disable()
		}

		labelConnect.SetText(stringConnected)
		labelServiceMode.SetText(stringService)
		labelVersion.SetText(stringVersion)
	}
}

func connectCAN() {
	err := errors.New("")
	for err != nil {
		can, err = b.Speed(ixxatvci3.Bitrate25kbps).Get()
		time.Sleep(200 * time.Millisecond)
	}
	can.Run()
	canOk <- 1
}

func processCAN() {
	<-canOk
	bOkCAN = true
	ch, _ := can.GetMsgChannelCopy()
	go threadActivity()
	go threadRequest()
	go threadUpdate()

	for msg := range ch {
		switch msg.ID {

		case COMPLITE:
			if msg.Len == 1 {
				compliteStage = false // получили сообщение о завершении этапа, перейти к следующему
			}

		case IPTM_GET_PARAM: // запрашиваем состояния настройки, включен ли сервисный режим
			if msg.Len == 5 {
				switch msg.Data[0] {
				case CFG_SVC:
					if msg.Data[1] == 1 {
						bServiceMode = true
					} else {
						bServiceMode = false
					}

				case CFG_IPTM_VERSION:
					VER_MAJOR = msg.Data[1]
					VER_MINOR = msg.Data[2]
					VER_PATCH = msg.Data[3]
				}
			}
		}
	}
}

// Проверяем наличие связи с ИПТМ по выдаваемым им сообщениям
func threadActivity() {
	for {
		_, err1 := can.GetMsgByID(KKM_DATA1, 2*time.Second)
		_, err2 := can.GetMsgByID(KKM_DATA2, 2*time.Second)
		if err1 != nil && err2 != nil {
			resetInfo()
		} else {
			bConnected = true
		}
	}
}

// Запрашиваем настройки установленные на ИПТМ
func threadRequest() {
	msg := candev.Message{ID: IPTM_GET_PARAM, Len: 1} //
	ids := []uint8{CFG_SVC, CFG_IPTM_VERSION}
	for {
		for i := range ids {
			if bConnected {
				msg.Data[0] = ids[i] //режим обсл
				can.Send(msg)
			}
			time.Sleep(1 * time.Second)
		}
	}
}

func enterServiceMode() {
	code := CODE
	msg := candev.Message{ID: IPTM_KEY, Len: 8, Data: [8]byte{
		uint8(code >> 56),
		uint8(code >> 48),
		uint8(code >> 40),
		uint8(code >> 32),
		uint8(code >> 24),
		uint8(code >> 16),
		uint8(code >> 8),
		uint8(code),
	}}

	for {
		for bConnected && !bServiceMode && stage != FINISH {

			can.Send(msg)
			time.Sleep(time.Second)
		}
		runtime.Gosched()
	}
}

// После нажатии кнопки устанавливаем stage = PREPARE_UPDATE, который запустит поток обновления
// после завершения этапа, ждем ответ от МК и переходим к следующему
func threadUpdate() {

	for {

		if !compliteStage { // ожидаем подтверждения после каждого этапа

			switch stage {

			case PREPARE_UPDATE:
				labelLoading.SetText("Подготовка к обновлению")
				msg := candev.Message{ID: UPDATE_ID, Len: PREPARE_UPDATE} // ожидаем подтверждения завершение очистки секторов
				can.Send(msg)
				fmt.Printf("PREPARE_UPDATE\r\n")
				stage = UPDATE
				compliteStage = true // этап завершен, ожидание подтверждения для перехода к следующему

			case UPDATE:

				allByte := len(dataFirmware)
				var txByteMsg int
				var txBytesAll int
				coefBar := allByte / 100
				ppbar.Hidden = false

				msg := candev.Message{ID: UPDATE_ID, Len: UPDATE} // ожидаем подтверждения завершение очистки секторов
				for numByte := range dataFirmware {
					txBytesAll++
					labelLoading.SetText(fmt.Sprintf("Передача данных %d из %d байт", txBytesAll, allByte))
					ppbarValue.Set(float64(txBytesAll / coefBar))

					msg.Data[txByteMsg] = dataFirmware[numByte]

					if txByteMsg == 7 {
						txByteMsg = 0
						can.Send(msg)
						compliteStage = true

					} else {
						txByteMsg++
					}
					for compliteStage {
						time.Sleep(5 * time.Millisecond)
					}
				}
				stage = FINISH_UPDATE

			case FINISH_UPDATE:
				ppbar.Hidden = true
				labelLoading.SetText("Применение изменений")
				time.Sleep(time.Second / 2)
				msg := candev.Message{ID: UPDATE_ID, Len: FINISH_UPDATE}
				can.Send(msg)
				stage = REBOOTUPDATE
				compliteStage = true

			case REBOOTUPDATE:
				labelLoading.SetText("Перезагрузка")
				time.Sleep(time.Second / 2)
				msg := candev.Message{ID: UPDATE_ID, Len: REBOOTUPDATE} //
				can.Send(msg)
				stage = FINISH
				compliteStage = true

			case FINISH:
				labelLoading.SetText("Обновление установленно")

			default:
				time.Sleep(time.Second)
			}

		} else {
			time.Sleep(time.Second)
		}
	}
}
