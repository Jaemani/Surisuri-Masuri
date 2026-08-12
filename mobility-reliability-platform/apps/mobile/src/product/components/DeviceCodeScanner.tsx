import { useState } from 'react';
import { CameraView, useCameraPermissions, type BarcodeScanningResult } from 'expo-camera';
import { Pressable, StyleSheet, Text, View } from 'react-native';
import { parseDeviceQrValue } from '../deviceCode';
import { colors } from './ProductUi';

export function DeviceCodeScanner({ onCode, onClose }: { onCode: (code: string) => void; onClose: () => void }) {
  const [permission, requestPermission] = useCameraPermissions();
  const [error, setError] = useState<string | null>(null);
  const [scanned, setScanned] = useState(false);
  const scan = ({ data }: BarcodeScanningResult) => {
    if (scanned) return;
    const code = parseDeviceQrValue(data);
    if (!code) { setError('수리수리마수리 기기 코드 형식이 아닙니다.'); return; }
    setScanned(true); onCode(code); onClose();
  };
  if (!permission) return <View style={styles.fallback}><Text style={styles.copy}>카메라 권한 상태를 확인하고 있어요.</Text><Pressable accessibilityRole="button" onPress={onClose} style={styles.close}><Text style={styles.closeText}>수동 입력으로 돌아가기</Text></Pressable></View>;
  if (!permission.granted) return <View style={styles.fallback}><Text accessibilityRole="header" style={styles.title}>QR 카메라 권한</Text><Text style={styles.copy}>기기 공개코드만 읽습니다. 권한 없이도 수동 입력할 수 있어요.</Text><Pressable accessibilityRole="button" onPress={() => void requestPermission()} style={styles.primary}><Text style={styles.primaryText}>카메라 권한 요청</Text></Pressable><Pressable accessibilityRole="button" onPress={onClose} style={styles.close}><Text style={styles.closeText}>수동 입력 사용</Text></Pressable></View>;
  return <View style={styles.scanner}><CameraView accessibilityLabel="기기 QR 스캐너" barcodeScannerSettings={{ barcodeTypes: ['qr'] }} onBarcodeScanned={scanned ? undefined : scan} style={StyleSheet.absoluteFillObject} /><View style={styles.frame} /><View style={styles.overlay}><Text style={styles.overlayTitle}>기기 QR을 사각형 안에 맞춰 주세요</Text>{error ? <Text accessibilityRole="alert" style={styles.error}>{error}</Text> : null}<Pressable accessibilityRole="button" onPress={onClose} style={styles.closeDark}><Text style={styles.closeDarkText}>취소하고 수동 입력</Text></Pressable></View></View>;
}

const styles = StyleSheet.create({fallback:{backgroundColor:colors.surface,borderColor:colors.border,borderRadius:16,borderWidth:1,marginTop:12,padding:16},title:{color:colors.ink,fontSize:18,fontWeight:'800'},copy:{color:colors.muted,fontSize:14,lineHeight:21,marginTop:7},primary:{alignItems:'center',backgroundColor:colors.teal,borderRadius:13,justifyContent:'center',marginTop:14,minHeight:48},primaryText:{color:'#FFF',fontSize:15,fontWeight:'800'},close:{alignItems:'center',justifyContent:'center',minHeight:44},closeText:{color:colors.tealDark,fontSize:14,fontWeight:'800'},scanner:{borderRadius:16,height:310,marginTop:12,overflow:'hidden'},frame:{alignSelf:'center',borderColor:'#FFF',borderRadius:16,borderWidth:3,height:180,marginTop:45,width:220},overlay:{alignItems:'center',backgroundColor:'rgba(16,36,40,.72)',bottom:0,left:0,padding:14,position:'absolute',right:0},overlayTitle:{color:'#FFF',fontSize:14,fontWeight:'800'},error:{color:'#FFD3BB',fontSize:13,fontWeight:'700',marginTop:5},closeDark:{marginTop:8,padding:7},closeDarkText:{color:'#C7E8DF',fontSize:13,fontWeight:'800'}});
