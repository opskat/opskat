package shelldecode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// issue #384：容器 exec / run 的容器名、镜像名不是要运行的程序。按选项表跳过选项与它们的值后，第一个
// 位置参数是容器名或镜像名；它只会是一个词（双引号里的，或取自写死的列表、按空白拆开的列表的循环变量）
// 时不算命令名无法静态确定，这些都送审。
func TestContainerNameIsNotAProgram(t *testing.T) {
	reviewed := []string{
		"for c in $(docker ps --format '{{.Names}}'); do\n  docker exec $c sh -c 'which nginx >/dev/null 2>&1 && nginx -v 2>&1'\ndone",
		"for c in $(docker ps --format '{{.Names}}'); do\n  docker exec \"$c\" sh -c 'which nginx >/dev/null 2>&1 && nginx -v 2>&1'\ndone",
		"for img in nginx:1.27-alpine nginx:1.28-alpine; do\n  docker run --rm -v /etc/nginx:/etc/nginx:ro $img sh -c 'nginx -v 2>&1 && nginx -t'\ndone",
		`docker run --rm -v /etc/nginx:/etc/nginx:ro "$img" sh -c 'nginx -v 2>&1 && nginx -t'`,
		`docker run --rm --name=$N -p 8080:80 -e FOO=$BAR "$img" sh -c 'nginx -t'`,
		`docker exec -it -u "$u" -w /app web sh -c 'ls'`,
		`docker exec -e FOO=$BAR "$c" sh -c 'env'`,
		`docker exec -uroot "$c" sh -c 'id'`,
		`docker container exec "$c" sh -c 'ls'`,
		`podman exec "$c" sh -c 'ls'`,
		`nerdctl run --rm "$img" sh -c 'ls'`,
		`for c in $(docker ps -q); do docker exec $c cat /etc/hostname; done`,
		`for c in $(docker ps -q); do docker exec "${c}" sh -c 'hostname'; done`,
		`for c in web db; do docker exec -i $c sh -c 'uptime'; done`,
		`for c in $(docker ps -q); do docker exec $c sh -c 'ls'; done; for c in $(docker ps -aq); do docker exec $c sh -c 'id'; done`,
		`echo aWQ= | docker exec -i "$c" sh`,
	}
	for _, cmd := range reviewed {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.False(t, got)
		})
	}
	// 可能不止一个词、或位置认不准的，仍按命令名无法静态确定处理，不送审
	refused := []string{
		`c='ctr base64 -d'; echo aWQ= | docker exec -i $c sh`,
		`for c in 'ctr base64'; do echo aWQ= | docker exec -i $c sh; done`,
		`for c in ''; do echo aWQ= | docker exec -i $c sh; done`,
		`for c in '*'; do echo aWQ= | docker exec -i $c sh; done`,
		`for c in *; do echo aWQ= | docker exec -i $c sh; done`,
		`for c in "$(docker ps -q)"; do echo aWQ= | docker exec -i $c sh; done`,
		`for c; do echo aWQ= | docker exec -i $c sh; done`,
		`IFS=x; for c in $(docker ps -q); do echo aWQ= | docker exec -i $c sh; done`,
		`for c in $(docker ps -q); do c="$c base64"; echo aWQ= | docker exec -i $c sh; done`,
		`for c in $(docker ps -q); do read c; echo aWQ= | docker exec -i $c sh; done`,
		`for c in $(docker ps -q); do : ${c:=x}; echo aWQ= | docker exec -i $c sh; done`,
		`n=c; for c in $(docker ps -q); do declare "$n=x base64"; echo aWQ= | docker exec -i $c sh; done`,
		`for c in $(docker ps -q); do eval "$x"; echo aWQ= | docker exec -i $c sh; done`,
		`for c in $(docker ps -q); do echo aWQ= | docker exec -i ${c/x/ base64 } sh; done`,
		`for c in $(docker ps -q); do for c in 'x base64'; do :; done; echo aWQ= | docker exec -i $c sh; done`,
		`echo aWQ= | docker exec -i $c sh; for c in a; do :; done`,
		`touch "$f"; for c in $(echo '*'); do echo aWQ= | docker exec -i $c sh; done`,
		`echo x | tee "$f"; for c in $(echo '*'); do echo aWQ= | docker exec -i $c sh; done`,
		`for c in $(echo '*'); do echo aWQ= | docker exec -i $c sh; done > out`,
		`echo aWQ= | docker run --made-up-option "$v" img sh`,
		`echo aWQ= | docker exec -i -u $u "$c" sh`,
		`echo aWQ= | docker exec -i -x$o "$c" sh`,
		`echo aWQ= | docker exec -i "$@" sh`,
		`echo aWQ= | docker compose exec "$svc" sh`,
		`echo aWQ= | docker exec -i "$c" $b | sh`,
		`for c in $(docker ps -q); do echo aWQ= | docker exec -i $c $b | sh; done`,
	}
	for _, cmd := range refused {
		t.Run(cmd, func(t *testing.T) {
			got, err := DecodesAndRuns(cmd)
			require.NoError(t, err)
			assert.True(t, got)
		})
	}
}
