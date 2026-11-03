/** 用途：逐项显示浏览器测试计划、开始和结果；由 Playwright 调用，无凭据或页面正文输出，报告文件仍由原有 reporter 保存。 */
class ChecklistReporter {
  /** 用途：接收完整测试树并编号；参数为配置及 suite，无返回；运行前公布待测项，标题用于业务核对。 */
  onBegin(config, suite) {
    this.tests = suite.allTests();
    this.config = config;
    for (const test of this.tests) this.print('待测', test);
  }
  /** 用途：输出稳定编号和业务标题；参数为状态及测试，无返回；仅输出测试定义，不打印请求或凭据。 */
  print(status, test) {
    const index = String(this.tests.indexOf(test) + 1).padStart(3, '0');
    const total = String(this.tests.length).padStart(3, '0');
    console.log('[浏览器' + status + ' ' + index + '/' + total + '] ' + test.title);
  }
  /** 用途：测试执行前立即显示测试内容；参数为测试，无返回；失败后的未执行项保留为待测。 */
  onTestBegin(test) { this.print('开始', test); }
  /** 用途：显示测试启动或全局钩子失败；参数 error 为运行器错误，返回无。
   * 仅打印首行并隐藏凭据/连接串，避免数据库超时被零执行统计掩盖。 */
  onError(error) {
    const message = String(error?.message || 'Unknown runner error').split('\n')[0]
      .replace(/(?:postgres(?:ql)?|redis):\/\/[^\s]+/gi, '[连接信息已隐藏]')
      .replace(/Bearer\s+[^\s]+/gi, 'Bearer [已隐藏]')
      .replace(/sk-[a-zA-Z0-9_-]+/g, '[凭据已隐藏]');
    console.error('[浏览器运行错误] ' + message);
  }
  /** 用途：区分通过、失败、中断和跳过；参数为测试与结果，无返回；跳过不能显示为通过，原始错误由报告文件保存。 */
  onTestEnd(test, result) {
    const names = {passed: '通过', failed: '失败', timedOut: '超时', skipped: '未执行', interrupted: '中断'};
    this.print(names[result.status] || result.status, test);
  }
  /** 用途：结束时核对执行证据；部分运行也不允许零执行/全跳过，完整运行另核对固定分母。
   * 参数 result 是 Playwright 结果；返回 failed 阻止假成功，报告保留每条状态，无凭据输出。 */
  onEnd(result) {
    if (!this.config?.rootDir) return;
    const path = require('node:path');
    const catalog = require('./scenario-catalog.json');
    const {reportCoverage} = require('./scenario-coverage.cjs');
    const report = reportCoverage(catalog, this.tests, path.resolve(process.env.E2E_RUN_DIR || '../.e2e/current', 'scenario-coverage.json'));
    console.log('业务场景覆盖：' + report.passed + '/' + report.total + '，未通过 ' + report.unmet.length);
    if (!this.tests.some(test => test.results.length === 1 && test.results[0].status === 'passed')) return {status: 'failed'};
    if (process.env.E2E_FULL_COVERAGE === '1' && (report.unmet.length || report.unregistered.length)) return {status: 'failed'};
  }
}
module.exports = ChecklistReporter;
