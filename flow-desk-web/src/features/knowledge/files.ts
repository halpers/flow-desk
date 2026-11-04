export async function readKnowledgeFile(file: File, ocrMode = "off") {
  const pdf = file.name.toLowerCase().endsWith(".pdf");
  if (!pdf && !/\.(txt|md)$/i.test(file.name))
    throw new Error("请选择 PDF、TXT 或 Markdown 文件");
  if (file.size > (pdf ? 2 * 1024 * 1024 : 256 * 1024))
    throw new Error(pdf ? "PDF 不能超过 2 MiB" : "文本文件不能超过 256 KiB");
  const bytes = new Uint8Array(await file.arrayBuffer());
  if (!pdf) {
    const content = new TextDecoder("utf-8", { fatal: true }).decode(bytes);
    return { filename: file.name, content };
  }
  let binary = "";
  for (let position = 0; position < bytes.length; position += 8192)
    binary += String.fromCharCode(...bytes.subarray(position, position + 8192));
  return {
    filename: file.name,
    content_base64: btoa(binary),
    ocr_mode: ocrMode,
  };
}

export const indexErrors: Record<string, string> = {
  PDF_INVALID: "PDF 损坏或格式不受支持，请重新导出后上传",
  PDF_ENCRYPTED: "暂不支持加密 PDF，请上传未加密版本",
  PDF_OCR_REQUIRED: "未提取到文字；扫描件需要先完成 OCR",
  PDF_LIMIT_EXCEEDED: "PDF 超出页数、文本或解析资源限制",
  PDF_PARSE_TIMEOUT: "PDF 解析超时，请拆分或简化文件后重试",
  PDF_OCR_UNAVAILABLE: "OCR 服务未就绪，请联系管理员检查识别组件",
  PDF_OCR_FAILED: "OCR 识别失败，请检查扫描质量后上传新版本",
  PDF_OCR_EMPTY: "OCR 未识别到文字，请上传清晰的印刷文档",
  PDF_OCR_TIMEOUT: "OCR 超时，请拆分文件后重试",
  PDF_OCR_PAGE_LIMIT: "一次最多识别 10 页，请拆分文件或使用自动模式",
  INDEX_FAILED: "索引失败，请检查文件后上传新版本",
};
