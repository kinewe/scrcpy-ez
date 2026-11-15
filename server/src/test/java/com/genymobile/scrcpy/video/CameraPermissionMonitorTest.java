package com.genymobile.scrcpy.video;

public class CameraPermissionMonitorTest {
    public static void main(String[] args) throws Exception {
        String pkg="com.xingin.xhs";
        if (!CameraPermissionMonitor.isRejection("Camera v31_1030: Access for \"com.xingin.xhs&com.xingin.redscanner.scanner.QrCodeScannerActivityV2\" has been restricted",pkg)) throw new AssertionError("attribution chain missed");
        if (!CameraPermissionMonitor.isRejection("REJECT device v31_1030 client for package com.xingin.xhs (PID 25045), Camera disabled by policy",pkg)) throw new AssertionError("policy missed");
        for (String line:new String[]{"Camera client com.xingin.xhs opened normally", "Camera com.xingin.xhs.clone permission denied", "Camera com.other permission denied", "Camera disabled by policy", "Permission denied to unrelated operation com.xingin.xhs"}) {
            if (CameraPermissionMonitor.isRejection(line,pkg)) throw new AssertionError("false match: "+line);
        }
        int[] notices={0};
        String denied="Camera com.xingin.xhs permission denied";
        CameraPermissionMonitor allowed=new CameraPermissionMonitor(pkg,()->false,()->++notices[0]);
        allowed.consume(denied);allowed.close();
        if(notices[0]!=0)throw new AssertionError("unconfirmed log caused permission advice");
        CameraPermissionMonitor rejected=new CameraPermissionMonitor(pkg,()->true,()->++notices[0]);
        rejected.consume(denied);rejected.consume(denied);
        if(notices[0]!=1)throw new AssertionError("notice not deduplicated");
        rejected.close();rejected.consume(denied);
        if(notices[0]!=1)throw new AssertionError("closed monitor reported");
        System.out.println("camera permission log matching: owned package, attribution suffix, denial reasons and unrelated applications passed");
    }
}
