import AppKit
import Foundation
func measure(_ work: () -> Void) -> Double {
    let start=ProcessInfo.processInfo.systemUptime
    for _ in 0..<1000 { work() }
    return (ProcessInfo.processInfo.systemUptime-start)*1000
}
for pixels in [64,128] {
    let rep=NSBitmapImageRep(bitmapDataPlanes:nil,pixelsWide:pixels,pixelsHigh:pixels,bitsPerSample:8,samplesPerPixel:4,hasAlpha:true,isPlanar:false,colorSpaceName:.deviceRGB,bytesPerRow:0,bitsPerPixel:0)!
    let length=rep.bytesPerRow*pixels
    let bytes=rep.bitmapData!
    for y in 0..<pixels { for x in 0..<pixels {
        let i=y*rep.bytesPerRow+x*4
        let inside=x<=y/2 && y<pixels*3/4
        bytes[i]=inside ? 255 : 0; bytes[i+1]=inside ? 255 : 0; bytes[i+2]=inside ? 255 : 0; bytes[i+3]=inside ? 255 : 0
    }}
    let saved=Data(bytes:bytes,count:length)
    var sink=0
    _=rep.representation(using:.png,properties:[:])!
    let pngMS=measure {
        let png=rep.representation(using:.png,properties:[:])!
        var hasher=Hasher();hasher.combine(png);hasher.combine(0.0);hasher.combine(0.0)
        sink ^= hasher.finalize()
    }
    let compareMS=measure {
        let current=Data(bytes:bytes,count:length)
        if current==saved { sink &+= 1 }
    }
    print("pixels=\(pixels) iterations=1000 png_and_hash_ms=\(pngMS) raster_copy_compare_ms=\(compareMS) raster_bytes=\(length) sink=\(sink)")
}
