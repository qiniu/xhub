"""真实 Nginx 慢读回归：本地容器内暂停接收，确认旧2秒断流、新配置完整发送；不访问生产。"""
import json
import pathlib
import subprocess
import tempfile
import unittest
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
NGINX = "nginx:1.28-alpine"
NODE = "node:24-bookworm"


class NginxSlowClientTest(unittest.TestCase):
    def test_client_resumes_after_two_seconds(self):
        """前置 Docker；真实TCP暂停3.5秒使写缓冲耗尽，旧配置失败、新片段保留连接；finally清除容器与临时文件。"""
        name = "xhub-slow-client-" + uuid.uuid4().hex[:10]
        with tempfile.TemporaryDirectory(prefix="xhub-nginx-") as directory:
            root = pathlib.Path(directory)
            body_size = 32 << 20
            (root / "body").write_bytes(b"x" * body_size)
            (root / "nginx.conf").write_text("""
events {}
http {
  access_log /dev/stdout;
  error_log /dev/stderr info;
  send_timeout 2s;
  server {
    listen 8080;
    location /old { alias /fixture/body; }
    location /fixed {
      alias /fixture/body;
      include /fixture/client-timeout.conf;
    }
  }
}
""")
            (root / "client-timeout.conf").write_text((ROOT / "deploy/nginx/xhub-client-timeout.conf").read_text())
            # 在相同网络命名空间运行客户端，避免宿主机端口转发提前吸收流量掩盖真实写阻塞。
            (root / "client.cjs").write_text("""
const http = require("node:http");
const assert = require("node:assert/strict");
// receive 接收指定路径：首块后暂停3.5秒，再读取剩余正文；硬期限12秒，失败也销毁连接。
function receive(path) {
  return new Promise((resolve,reject) => {
    let bytes=0, paused=false, status=0;
    const req=http.get("http://127.0.0.1:8080/"+path, res => {
      status=res.statusCode;
      res.on("data", chunk => {
        bytes+=chunk.length;
        if (!paused) { paused=true; res.pause(); setTimeout(()=>res.resume(),3500); }
      });
      res.on("error",error => {
        if(error.code!=="ECONNRESET") reject(error);
      });
      res.on("close",()=>resolve({path,status,bytes,complete:res.complete}));
    });
    const timer=setTimeout(()=>req.destroy(new Error("slow read deadline")),12000);
    req.on("close",()=>clearTimeout(timer)); req.on("error",reject);
  });
}
(async()=>{
  const old=await receive("old");
  const fixed=await receive("fixed");
  console.log(JSON.stringify({old,fixed}));
  assert.equal(old.status,200); assert.equal(old.complete,false,"旧2秒配置必须真实断流");
  assert.ok(old.bytes<32*1024*1024);
  assert.equal(fixed.status,200); assert.equal(fixed.complete,true,"配置必须允许恢复读取");
  assert.equal(fixed.bytes,32*1024*1024,"正文必须完整");
})().catch(error=>{console.error(error);process.exitCode=1;});
""")
            try:
                subprocess.run(["docker", "run", "-d", "--name", name, "-v", f"{root}:/fixture:ro",
                                NGINX, "nginx", "-c", "/fixture/nginx.conf", "-g", "daemon off;"],
                               check=True, capture_output=True, text=True)
                subprocess.run(["docker", "exec", name, "nginx", "-t", "-c", "/fixture/nginx.conf"], check=True)
                run = subprocess.run(["docker", "run", "--rm", "--network", f"container:{name}",
                                      "-v", f"{root}:/fixture:ro", NODE, "node", "/fixture/client.cjs"],
                                     capture_output=True, text=True, timeout=45)
                print(run.stdout, flush=True)
                self.assertEqual(run.returncode, 0, run.stderr)
                result = json.loads(run.stdout)
                self.assertEqual(result["fixed"]["bytes"], body_size)
                logs = subprocess.run(["docker", "logs", name], capture_output=True, text=True, check=True)
                self.assertIn("client timed out", logs.stderr, "必须验证真实Nginx写超时原因，而非任意断线")
            finally:
                subprocess.run(["docker", "rm", "-f", name], check=True, capture_output=True)


if __name__ == "__main__":
    unittest.main()
