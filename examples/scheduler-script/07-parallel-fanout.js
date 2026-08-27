// 并行扇出：一批 prompt 同时发给 LLM，总耗时接近最慢的那一次，而不是所有次数之和。
//
// 对照写法（串行，逐个等待）：
//   for (const topic of topics) results.push(scheduler.llm(topic).text);

const TOPICS = ["产品反馈", "线上故障", "成本异常"];

function summarizePrompt(topic) {
  return `用一句话总结「${topic}」本周的关键变化`;
}

async function main() {
  // 每次 .async 调用立即返回一个 Promise，请求已经在后台进行；
  // 到 Promise.all 收集时三个请求早已同时在飞。
  const summaries = await Promise.all(
    TOPICS.map((topic) => scheduler.llm.async(summarizePrompt(topic)))
  );

  // 部分失败不应该让整批丢失，所以用 allSettled 单独跑一批可选的补充调用。
  const enrichment = await Promise.allSettled(
    TOPICS.map((topic) => scheduler.llm.async(`列出「${topic}」的三个风险点`))
  );

  const result = {
    summaries: summaries.map((item, index) => ({
      topic: TOPICS[index],
      text: item.text,
    })),
    enrichedCount: enrichment.filter((item) => item.status === "fulfilled").length,
  };

  scheduler.log("parallel fanout completed", result);
  return result;
}
