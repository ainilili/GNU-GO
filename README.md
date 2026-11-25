# GNU-GO

9 路围棋。可以在平坦蒙特卡洛、搜索树、UCT+RAVE 之间切换，也可以让选中的算法对 GNU Go 的 9 路引擎。

终局用中国规则数子，贴 7.5 目。规则说明见 `说明.md`。

```
go test ./...
go run .
```

页面在 http://127.0.0.1:8791 。`web/gnugo` 里的引擎文件来自 [abbychau/GNUgo](https://github.com/abbychau/GNUgo) 的公开页面。
