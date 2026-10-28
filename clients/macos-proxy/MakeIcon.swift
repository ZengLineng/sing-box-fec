import AppKit

let destination = URL(fileURLWithPath: CommandLine.arguments[1])
try FileManager.default.createDirectory(at: destination, withIntermediateDirectories: true)
for size in [16, 32, 64, 128, 256, 512, 1024] {
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    bitmap.size = NSSize(width: size, height: size)
    NSGraphicsContext.saveGraphicsState()
    NSGraphicsContext.current = NSGraphicsContext(bitmapImageRep: bitmap)
    NSColor(calibratedRed: 0.14, green: 0.42, blue: 0.34, alpha: 1).setFill()
    NSBezierPath(roundedRect: NSRect(x: 0, y: 0, width: size, height: size), xRadius: CGFloat(size) * 0.22, yRadius: CGFloat(size) * 0.22).fill()
    let text = "F" as NSString
    let attributes: [NSAttributedString.Key: Any] = [.font: NSFont.boldSystemFont(ofSize: CGFloat(size) * 0.72), .foregroundColor: NSColor.white]
    let textSize = text.size(withAttributes: attributes)
    text.draw(at: NSPoint(x: (CGFloat(size) - textSize.width) / 2, y: (CGFloat(size) - textSize.height) / 2), withAttributes: attributes)
    NSGraphicsContext.restoreGraphicsState()
    let png = bitmap.representation(using: .png, properties: [:])!
    if [16, 32, 128, 256, 512].contains(size) { try png.write(to: destination.appendingPathComponent("icon_\(size)x\(size).png")) }
    if [32, 64, 256, 512, 1024].contains(size) { try png.write(to: destination.appendingPathComponent("icon_\(size/2)x\(size/2)@2x.png")) }
}
