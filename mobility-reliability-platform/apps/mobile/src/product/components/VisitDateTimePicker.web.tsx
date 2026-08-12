import { Pressable, StyleSheet, Text, View } from 'react-native';

import { formatScheduleSeoul } from '../schedule';
import { colors } from './ProductUi';

type Props = { value: Date; minimumDate: Date; maximumDate: Date; onChange: (value: Date) => void };

export function VisitDateTimePicker({ value, minimumDate, maximumDate, onChange }: Props) {
  const move = (milliseconds: number) => onChange(new Date(Math.min(maximumDate.getTime(), Math.max(minimumDate.getTime(), value.getTime() + milliseconds))));
  return <View>
    <View testID="repairer-schedule-summary" style={styles.summary}><Text style={styles.summaryLabel}>선택한 방문 일정</Text><Text accessibilityLiveRegion="polite" style={styles.summaryValue}>{formatScheduleSeoul(value)}</Text></View>
    <Text style={styles.help}>웹 미리보기에서는 아래 버튼으로 조정합니다. Android와 iPhone에서는 기기의 날짜·시간 선택기가 열립니다.</Text>
    <View style={styles.actions}><Pressable accessibilityRole="button" onPress={() => move(-86_400_000)} style={styles.button}><Text style={styles.buttonText}>하루 전</Text></Pressable><Pressable accessibilityRole="button" onPress={() => move(86_400_000)} style={styles.button}><Text style={styles.buttonText}>하루 뒤</Text></Pressable></View>
    <View style={styles.actions}><Pressable accessibilityRole="button" onPress={() => move(-1_800_000)} style={styles.button}><Text style={styles.buttonText}>30분 전</Text></Pressable><Pressable accessibilityRole="button" onPress={() => move(1_800_000)} style={styles.button}><Text style={styles.buttonText}>30분 뒤</Text></Pressable></View>
  </View>;
}

const styles = StyleSheet.create({
  summary:{backgroundColor:colors.canvas,borderRadius:13,marginTop:16,padding:14},summaryLabel:{color:colors.muted,fontSize:13,fontWeight:'700'},summaryValue:{color:colors.ink,fontSize:17,fontWeight:'800',marginTop:5},help:{color:colors.muted,fontSize:12,lineHeight:18,marginTop:9},actions:{flexDirection:'row',gap:9,marginTop:9},button:{alignItems:'center',backgroundColor:colors.tealWash,borderColor:colors.teal,borderRadius:12,borderWidth:1,flex:1,justifyContent:'center',minHeight:44},buttonText:{color:colors.tealDark,fontSize:13,fontWeight:'800'},
});
