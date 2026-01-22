module github.com/lazzerex/aegis/control-plane

go 1.23.0

require (
    github.com/go-chi/chi/v5 v5.0.11
    github.com/lazzerex/aegis/control-plane/proto v0.0.0-00010101000000-000000000000
    github.com/prometheus/client_golang v1.18.0
    go.uber.org/zap v1.26.0
    google.golang.org/grpc v1.62.0
    gopkg.in/yaml.v3 v3.0.1
)

replace github.com/lazzerex/aegis/control-plane/proto => ./proto

