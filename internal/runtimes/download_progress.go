package runtimes

type DownloadProgressState string

const (
	DownloadProgressStarted   DownloadProgressState = "started"
	DownloadProgressAdvanced  DownloadProgressState = "advanced"
	DownloadProgressCompleted DownloadProgressState = "completed"
	DownloadProgressFailed    DownloadProgressState = "failed"
)

type DownloadProgressEvent struct {
	State        DownloadProgressState
	Runtime      string
	Version      string
	URL          string
	Attempt      int
	MaxAttempts  int
	WrittenBytes int64
	TotalBytes   int64
	WillRetry    bool
	Err          error
}

type DownloadProgressObserver interface {
	OnDownloadProgress(event DownloadProgressEvent)
}

func (m *Manager) SetDownloadProgressObserver(observer DownloadProgressObserver) {
	m.downloadObserver = observer
}

func (m *Manager) emitDownloadProgress(event DownloadProgressEvent) {
	if m == nil || m.downloadObserver == nil {
		return
	}
	m.downloadObserver.OnDownloadProgress(event)
}
